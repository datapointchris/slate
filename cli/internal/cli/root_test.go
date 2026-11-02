package cli

import (
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/datapointchris/goselfupdate/autoupdate"
)

func TestExitCodeFor(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"exitCode carries its own value", exitCode(1), 1},
		{"generic runtime error is 1", errors.New("boom"), 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := exitCodeFor(c.err); got != c.want {
				t.Errorf("exitCodeFor(%v) = %d, want %d", c.err, got, c.want)
			}
		})
	}
}

// runLine drives the real tree through run, the path the shipped binary takes,
// with the version check suppressed. goclikit.Execute resolves the command
// from os.Args, so the line goes there rather than through SetArgs.
func runLine(t *testing.T, args ...string) error {
	t.Helper()
	original := os.Args
	os.Args = append([]string{"slate"}, args...)
	t.Cleanup(func() { os.Args = original })

	root := NewRootCommand()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	return run(root, autoupdate.Config{Suppress: true})
}

// A word one slip from a subcommand, or one its SuggestFor lists, is answered
// with the subcommand, at the root and inside a group alike.
func TestAnUnknownSubcommandNamesTheNearOnes(t *testing.T) {
	for _, c := range []struct {
		args  []string
		meant string
	}{
		{[]string{"issuez"}, "issues"},
		{[]string{"issues", "lisy"}, "list"},
		{[]string{"items", "show", "848"}, "issues"},
		{[]string{"show", "848", "--json"}, "issues"},
		{[]string{"search", "golang"}, "issues"},
		{[]string{"issues", "initiativez", "--json"}, "initiatives"},
		{[]string{"issues", "labels", "lisz", "--json"}, "list"},
	} {
		err := runLine(t, c.args...)
		if err == nil || !slices.Contains(strings.Fields(err.Error()), c.meant) {
			t.Errorf("%v answered %v, want it to name %q", c.args, err, c.meant)
		}
	}
}

// A flag the leaf would have taken, typed after a word naming no subcommand,
// leaves the word as the mistake at every depth.
func TestAFlagAfterAnUnknownWordRefusesTheWord(t *testing.T) {
	for _, args := range [][]string{
		{"bogus", "search", "foo", "--json"},
		{"issues", "bogus", "--json"},
		{"issues", "initiatives", "bogus", "5", "--json"},
		{"issues", "labels", "bogus", "--json"},
	} {
		err := runLine(t, args...)
		if err == nil || !strings.Contains(err.Error(), `unknown command "bogus"`) {
			t.Errorf("%v answered %v, want it to refuse \"bogus\"", args, err)
		}
		if got := exitCodeFor(err); got != 2 {
			t.Errorf("%v exit = %d, want 2", args, got)
		}
	}
}

// cobra answers --help before it parses anything else, so a mistyped word
// asking for help would print the group's screen and exit 0 as though the word
// had been a subcommand.
func TestAWordTypedWithHelpIsRefusedAtEveryGroup(t *testing.T) {
	for _, args := range [][]string{
		{"issues", "bogus", "--help"},
		{"issues", "initiatives", "bogus", "--help"},
		{"issues", "labels", "bogus", "-h"},
	} {
		err := runLine(t, args...)
		if err == nil || !strings.Contains(err.Error(), `unknown command "bogus"`) {
			t.Errorf("%v answered %v, want it to refuse \"bogus\"", args, err)
		}
		if got := exitCodeFor(err); got != 2 {
			t.Errorf("%v exit = %d, want 2", args, got)
		}
	}
}

// runTree drives the real tree through run, as runLine does, and returns the
// exit code Execute would.
func runTree(t *testing.T, args ...string) int {
	t.Helper()
	return exitCodeFor(runLine(t, args...))
}

func TestCommandTree_ExitCodes(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"bare root shows help (success)", nil, 0},
		{"bare auth shows help (success)", []string{"auth"}, 0},
		{"bare issues shows help (success)", []string{"issues"}, 0},
		{"bare issues initiatives shows help (success)", []string{"issues", "initiatives"}, 0},
		{"bare issues labels shows help (success)", []string{"issues", "labels"}, 0},
		{"unknown top-level command is usage error", []string{"nope"}, 2},
		{"unknown auth subcommand is usage error", []string{"auth", "nope"}, 2},
		{"unknown issues subcommand is usage error", []string{"issues", "nope"}, 2},
		{"unknown flag is usage error", []string{"--nonsense"}, 2},
		{"auth login rejects a second positional arg", []string{"auth", "login", "https://a.example", "extra"}, 2},
		{"issues show without a number is usage error", []string{"issues", "show"}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runTree(t, c.args...); got != c.want {
				t.Errorf("args %v exit = %d, want %d", c.args, got, c.want)
			}
		})
	}
}
