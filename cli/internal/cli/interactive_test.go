package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
	"github.com/datapointchris/slate/cli/internal/prompt"
)

// answerCommand builds a bare command carrying create flags, so the
// flag-to-answer plumbing is testable without standing up an API client.
func answerCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.Flags().String("title", "", "")
	cmd.Flags().String("type", "task", "")
	cmd.Flags().String("description", "", "")
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	return cmd
}

// typeFields is the part of `issues create`'s form that holds a closed
// vocabulary, built without the API the full form reads its other fields from.
func typeFields() []prompt.Field {
	return []prompt.Field{
		{Key: "title", Label: "Title"},
		{Key: "type", Label: "Type", Choices: api.IssueTypes, Validate: prompt.OneOf(api.IssueTypes)},
		{Key: "description", Label: "Description", Optional: true, Multiline: true},
	}
}

func TestFlagAnswers_TakesOnlyWhatWasPassed(t *testing.T) {
	cmd := answerCommand(t, "--title", "Retry the upload")

	answers := flagAnswers(cmd, "title", "type", "description")

	if answers.Get("title") != "Retry the upload" {
		t.Errorf("title = %q, want the flag's value", answers.Get("title"))
	}
	if answers.Has("type") {
		t.Error("type was answered by its default — an unpassed flag is a question, not an answer")
	}
	if answers.Has("description") {
		t.Error("description was answered by a flag nobody passed")
	}
}

func TestFlagAnswers_AnEmptyFlagIsNotAnAnswer(t *testing.T) {
	cmd := answerCommand(t, "--title", "")

	if flagAnswers(cmd, "title").Has("title") {
		t.Error("--title \"\" counted as an answer — there is no field it could fill")
	}
}

func TestFlagAnswers_ARepeatableFlagContributesEveryValue(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().StringArray("label", nil, "")
	if err := cmd.ParseFlags([]string{"--label", "area-api", "--label", "needs-design"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	got := strings.Join(flagAnswers(cmd, "label").All("label"), "|")

	if got != "area-api|needs-design" {
		t.Errorf("label = %q, want both values in order", got)
	}
}

func TestValidateAnswers_RewritesToTheCanonicalSpelling(t *testing.T) {
	answers := prompt.Answers{"type": {"BUG"}}

	if err := validateAnswers(answers, typeFields()); err != nil {
		t.Fatalf("validateAnswers rejected a known type: %v", err)
	}
	if answers.Get("type") != "bug" {
		t.Errorf("type = %q, want the vocabulary's spelling", answers.Get("type"))
	}
}

func TestValidateAnswers_RejectionNamesTheFlagAndTheAcceptedValues(t *testing.T) {
	err := validateAnswers(prompt.Answers{"type": {"bgu"}}, typeFields())

	if err == nil {
		t.Fatal("validateAnswers accepted a type the vocabulary does not hold")
	}
	if !strings.Contains(err.Error(), "--type") {
		t.Errorf("error = %q, want it to name the flag", err)
	}
	if !strings.Contains(err.Error(), "bug") {
		t.Errorf("error = %q, want it to list what would have worked", err)
	}
}

func TestUnanswered_DropsWhatTheFlagsSupplied(t *testing.T) {
	fields := unanswered(typeFields(), prompt.Answers{"title": {"Retry"}})

	var keys []string
	for _, field := range fields {
		keys = append(keys, field.Key)
	}
	want := []string{"type", "description"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Errorf("fields = %v, want %v — a flag already passed is not a question", keys, want)
	}
}

func TestMissingFlags_NamesEveryRequiredFieldNobodyAnswered(t *testing.T) {
	missing := missingFlags(prompt.Answers{"title": {"Retry"}}, "title", "type")

	if strings.Join(missing, ",") != "--type" {
		t.Errorf("missing = %v, want just --type", missing)
	}
}

func TestLooksLikeUUID(t *testing.T) {
	cases := map[string]bool{
		"3f2504e0-4f89-11d3-9a0c-0305e82c3301": true,
		"3F2504E0-4F89-11D3-9A0C-0305E82C3301": true,
		"3f2504e0-4f89-11d3-9a0c-0305e82c330":  false, // too short
		"3f2504e04f8911d39a0c0305e82c3301":     false, // no dashes
		"zf2504e0-4f89-11d3-9a0c-0305e82c3301": false, // not hex
		"area-api":                             false,
	}
	for value, want := range cases {
		if got := looksLikeUUID(value); got != want {
			t.Errorf("looksLikeUUID(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestIssuesCreate_WithoutATerminalRefusesBeforeReachingTheAPI(t *testing.T) {
	noInput = false
	unreachableAPI(t) // any call here would fail loudly
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetIn(strings.NewReader(""))
	root.SetArgs([]string{"issues", "create"})

	err := root.Execute()

	if err == nil {
		t.Fatal("create returned no error — prompting a non-TTY caller blocks on a stdin that never closes")
	}
	if exitCodeFor(err) != 2 {
		t.Errorf("exit code = %d, want 2 — a missing flag is a usage mistake, not an API failure", exitCodeFor(err))
	}
	if !strings.Contains(err.Error(), "--title") {
		t.Errorf("error = %q, want it to name --title", err)
	}
	if strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("error = %q, want the local refusal to come before any request", err)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing written before the refusal", out.String())
	}
}
