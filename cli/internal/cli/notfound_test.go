package cli

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/goselfupdate/autoupdate"

	"github.com/datapointchris/slate/cli/internal/api"
)

// apiNotFound is the error shape the api package produces for a 404, with the
// subject the API reports.
func apiNotFound(subject string) error {
	return &api.APIError{StatusCode: http.StatusNotFound, Status: "404 Not Found", Message: subject}
}

// hintsFor reads the recovery hints the nearest annotated ancestor carries.
//
// The test's own reader rather than the library's, so a sweep asserts on the
// annotation this repo writes rather than on a function that would answer the
// same whether or not the annotation was there.
func hintsFor(cmd *cobra.Command) []string {
	for current := cmd; current != nil; current = current.Parent() {
		if joined := current.Annotations[goclikit.RecoveryHintsAnnotation]; joined != "" {
			return strings.Split(joined, "\n")
		}
	}
	return nil
}

// findCommand resolves a command path against the real assembled tree, so a
// test asserts on what `slate` actually runs rather than a fixture of it.
func findCommand(t *testing.T, path ...string) *cobra.Command {
	t.Helper()
	cmd, _, err := NewRootCommand().Find(path)
	if err != nil {
		t.Fatalf("Find(%v): %v", path, err)
	}
	if strings.Join(path, " ") != strings.TrimPrefix(cmd.CommandPath(), "slate ") {
		t.Fatalf("Find(%v) resolved to %q", path, cmd.CommandPath())
	}
	return cmd
}

// runWith drives the real tree with the version check suppressed, standing the
// leaf's RunE in for the API call so the failure under test is a 404 arriving
// where a 404 really arrives.
//
// The leaf is whatever cobra resolves argv to, rather than a path the test
// names separately. Any disagreement between the two would make the test
// assert against a command the tool would not have run.
func runWith(t *testing.T, failure error, argv ...string) error {
	t.Helper()

	original := os.Args
	os.Args = append([]string{"slate"}, argv...)
	t.Cleanup(func() { os.Args = original })

	root := NewRootCommand()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	leaf, _, err := root.Find(argv)
	if err != nil {
		t.Fatalf("Find(%v): %v", argv, err)
	}
	if leaf.RunE == nil {
		t.Fatalf("Find(%v) resolved to %q, which has no RunE", argv, leaf.CommandPath())
	}
	leaf.RunE = func(*cobra.Command, []string) error { return failure }

	return run(root, autoupdate.Config{Suppress: true})
}

// The one WithNotFound in run() is the whole feature reaching a terminal, and
// every test of the classifier alone stays green without it.
func TestA404FromTheRealTreeCarriesItsRecoveryHints(t *testing.T) {
	err := runWith(t, apiNotFound("issue 999999 not found"), "issues", "show", "999999")
	if err == nil {
		t.Fatal("expected the 404")
	}

	got := err.Error()
	for _, want := range []string{
		"issue 999999 not found",
		"slate show <number>",
		"slate search <words>",
		"slate issues list --status all",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("error = %q, want it to name %q", got, want)
		}
	}
}

// The transport prefix reports how the answer arrived. A 404 is an answer, and
// the line is read by someone who wants the next command rather than the status
// code that produced it.
func TestA404DropsTheTransportPrefix(t *testing.T) {
	err := runWith(t, apiNotFound("issue 999999 not found"), "issues", "show", "999999")

	if strings.Contains(err.Error(), "API request failed") {
		t.Errorf("error = %q, want no transport prefix", err)
	}
}

func TestARuntimeFailureIsUntouched(t *testing.T) {
	boom := errors.New("connection refused")
	err := runWith(t, boom, "issues", "show", "1")

	if !errors.Is(err, boom) {
		t.Fatalf("a non-404 was rewritten: %v", err)
	}
	if err.Error() != boom.Error() {
		t.Errorf("error = %q, want it left alone", err)
	}
}

// A 401 has a different remedy, and rewriting it would bury the one command
// that fixes it.
func TestAnotherStatusIsUntouched(t *testing.T) {
	unauthorized := &api.APIError{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized", Message: "nope"}
	err := runWith(t, unauthorized, "issues", "show", "1")

	if err.Error() != unauthorized.Error() {
		t.Errorf("error = %q, want the 401 left alone", err)
	}
}

// A proxy error page or a login redirect decodes to a status and nothing
// else, and the resource was never reached. A wrong api address and a missing
// item have different remedies.
func TestABodylessNotFoundKeepsItsStatusLine(t *testing.T) {
	bare := &api.APIError{StatusCode: http.StatusNotFound, Status: "404 Not Found"}
	err := runWith(t, bare, "issues", "show", "1")

	if err.Error() != bare.Error() {
		t.Errorf("error = %q, want the bare status line", err)
	}
}

// Callers branch on the status code, so the rewritten error has to keep the
// original reachable rather than only rendering it.
func TestTheAPIErrorStaysReachable(t *testing.T) {
	err := runWith(t, apiNotFound("issue 999999 not found"), "issues", "show", "999999")

	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As no longer reaches the *api.APIError: %v", err)
	}
	if !apiErr.NotFound() {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

// The classifier is the only half of the feature this repo owns.
func TestTheClassifierReportsOnlyABodiedNotFound(t *testing.T) {
	cases := map[string]struct {
		err     error
		subject string
		ok      bool
	}{
		"a 404 with a message": {apiNotFound("item 7 not found"), "item 7 not found", true},
		"a 404 with none":      {&api.APIError{StatusCode: 404, Status: "404 Not Found"}, "", true},
		"a 401":                {&api.APIError{StatusCode: 401, Status: "401 Unauthorized"}, "", false},
		"not an APIError":      {errors.New("connection refused"), "", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			subject, ok := notFound(tc.err)
			if subject != tc.subject || ok != tc.ok {
				t.Errorf("notFound = (%q, %v), want (%q, %v)", subject, ok, tc.subject, tc.ok)
			}
		})
	}
}

// The setup commands reach no record, and search, vocabulary and tui name none, so
// none of them has a missing record to recover from.
var resourceExempt = map[string]bool{
	"auth":       true,
	"config":     true,
	"update":     true,
	"search":     true,
	"vocabulary": true,
	"tui":        true,
}

// A resource added without hints is the way this regresses: the code keeps
// working and its 404 quietly goes back to naming no next command.
func TestEveryResourceCommandCarriesNotFoundHints(t *testing.T) {
	root := NewRootCommand()
	for _, group := range root.Commands() {
		if resourceExempt[group.Name()] {
			continue
		}
		walkCommands(group, func(cmd *cobra.Command) {
			hints := hintsFor(cmd)
			if len(hints) == 0 {
				t.Errorf("%s has no not-found hints", cmd.CommandPath())
				return
			}
			for _, hint := range hints {
				_, command, found := strings.Cut(hint, ": ")
				if !found || !strings.HasPrefix(command, "slate ") {
					t.Errorf("%s hint %q is not a sentence, a colon, and a slate command",
						cmd.CommandPath(), hint)
				}
			}
		})
	}
}

func walkCommands(cmd *cobra.Command, visit func(*cobra.Command)) {
	visit(cmd)
	for _, sub := range cmd.Commands() {
		walkCommands(sub, visit)
	}
}

// hintCommand returns the command half of a hint — everything after the colon.
func hintCommand(t *testing.T, hint string) string {
	t.Helper()
	_, command, found := strings.Cut(hint, ": ")
	if !found {
		t.Fatalf("hint %q has no colon", hint)
	}
	return command
}

// childNamed returns the subcommand of cmd with this name, or nil.
func childNamed(cmd *cobra.Command, name string) *cobra.Command {
	for _, sub := range cmd.Commands() {
		if sub.Name() == name {
			return sub
		}
	}
	return nil
}

// A hint names a command the reader is about to type, so it has to resolve
// against the tree. Renaming a subcommand otherwise leaves the hint pointing at
// nothing, and the shape check alone stays green.
func TestEveryHintNamesACommandThatResolves(t *testing.T) {
	root := NewRootCommand()
	walkCommands(root, func(cmd *cobra.Command) {
		for _, hint := range hintsFor(cmd) {
			command := hintCommand(t, hint)
			words := strings.Fields(strings.TrimPrefix(command, "slate "))
			current, consumed := root, 0
			for _, word := range words {
				next := childNamed(current, word)
				if next == nil {
					break
				}
				current, consumed = next, consumed+1
			}
			if consumed == 0 {
				t.Errorf("%s: hint %q names no command in the tree", cmd.CommandPath(), command)
				continue
			}
			// What is left has to be a flag, its value, or a <placeholder> —
			// anything else is a word that used to name a subcommand.
			previousWasFlag := false
			for _, word := range words[consumed:] {
				isFlag := strings.HasPrefix(word, "-")
				if !isFlag && !strings.HasPrefix(word, "<") && !previousWasFlag {
					t.Errorf("%s: hint %q trails %q, which names no subcommand of %s",
						cmd.CommandPath(), command, word, current.CommandPath())
				}
				previousWasFlag = isFlag
			}
		}
	})
}
