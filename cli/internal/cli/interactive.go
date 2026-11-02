package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/datapointchris/slate/cli/internal/prompt"
)

// errAborted is a form the user canceled. Abandoning a record half-typed is a
// choice, not a failure, so the command reports it and exits 0.
//
// Lead the report with a newline: Ctrl-C leaves the cursor part-way along the
// prompt it interrupted, and the terminal is out of raw mode by then, so the
// form itself can no longer move it.
var errAborted = errors.New("aborted")

// runForm asks the fields the flags left out and returns what was answered.
//
// Call it only behind interactive(cmd) — the same TTY and --no-input gate
// confirm uses. A command that prompts a caller which cannot answer blocks on a
// stdin that never closes, with no output and no exit code.
func runForm(cmd *cobra.Command, form prompt.Form) (prompt.Answers, error) {
	file, ok := cmd.InOrStdin().(*os.File)
	if !ok {
		return nil, fmt.Errorf("stdin is not a terminal")
	}
	session, err := prompt.NewTerminalSession(file, cmd.ErrOrStderr())
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()

	answers, err := form.Run(session)
	if errors.Is(err, io.EOF) {
		return nil, errAborted
	}
	return answers, err
}

// flagAnswers collects the named flags the caller actually passed, in the shape
// a form returns. Merging the two leaves one source of values, so nothing
// downstream has to ask which door a field came in through.
//
// A repeatable flag contributes every value it was given, which is why an
// answer is a list. A flag set to the empty string counts as unset: there is no
// field it could answer, and treating it as one would send a task with no name.
func flagAnswers(cmd *cobra.Command, keys ...string) prompt.Answers {
	answers := prompt.Answers{}
	for _, key := range keys {
		flag := cmd.Flags().Lookup(key)
		if flag == nil || !flag.Changed {
			continue
		}
		if values := flagValues(flag); len(values) > 0 {
			answers[key] = values
		}
	}
	return answers
}

// flagValues reads one flag as the list of non-empty values it holds.
func flagValues(flag *pflag.Flag) []string {
	raw := []string{flag.Value.String()}
	if slice, ok := flag.Value.(pflag.SliceValue); ok {
		raw = slice.GetSlice()
	}
	var values []string
	for _, value := range raw {
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

// unanswered returns the fields nothing has answered yet, which is the form to
// ask. A flag already passed is not a question.
func unanswered(fields []prompt.Field, answers prompt.Answers) []prompt.Field {
	var ask []prompt.Field
	for _, field := range fields {
		if !answers.Has(field.Key) {
			ask = append(ask, field)
		}
	}
	return ask
}

// missingFlags returns the flag spellings of the required keys nothing answered,
// so a non-interactive caller is told which flag to pass rather than that it
// cannot be asked.
func missingFlags(answers prompt.Answers, keys ...string) []string {
	var missing []string
	for _, key := range keys {
		if !answers.Has(key) {
			missing = append(missing, "--"+key)
		}
	}
	return missing
}

// validateAnswers applies each field's Validate to every value that answered it,
// rewriting each to the canonical spelling. Running the form's own validators
// over the flags is what keeps one bad value reading the same through either
// door.
func validateAnswers(answers prompt.Answers, fields []prompt.Field) error {
	for _, field := range fields {
		if field.Validate == nil || !answers.Has(field.Key) {
			continue
		}
		values := append([]string(nil), answers.All(field.Key)...)
		for i, value := range values {
			canonical, err := field.Validate(value)
			if err != nil {
				return fmt.Errorf("--%s: %w", field.Key, err)
			}
			values[i] = canonical
		}
		answers[field.Key] = values
	}
	return nil
}
