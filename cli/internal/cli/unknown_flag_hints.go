package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// withFlagValues declares the closed set of values flag takes. The shell
// completes them, and an unknown flag spelled as one of them is answered with
// the flag that takes it: `--all` names `--status all`.
func withFlagValues(cmd *cobra.Command, flag string, values ...string) {
	if err := cmd.RegisterFlagCompletionFunc(flag, cobra.FixedCompletions(values, cobra.ShellCompDirectiveNoFileComp)); err != nil {
		panic(fmt.Sprintf("%s --%s: %v", cmd.CommandPath(), flag, err))
	}
}

// jobsOfAnotherCommand names, by command path and then flag name, the command
// that does what a flag the command does not take would have done. An
// initiative's place in the queue is moved relative to another, so no flag on
// create or edit sets it.
var jobsOfAnotherCommand = map[string]map[string]string{
	"slate initiatives create": {"position": "slate initiatives reorder <initiative> --before <initiative> (or --after)"},
	"slate initiatives edit":   {"position": "slate initiatives reorder <initiative> --before <initiative> (or --after)"},
}

// flagsSpelledOtherwise names, by command path and then flag name, the flag a
// command takes for the field another noun's flag of that name sets. An issue
// leads with its title and an initiative with its name, so `--title` on an
// initiative meant `--name`.
var flagsSpelledOtherwise = map[string]map[string]string{
	"slate initiatives create": {"title": "name"},
	"slate initiatives edit":   {"title": "name"},
}

// explainUnknownFlag names the command that does what an unknown long flag on
// cmd was typed for, where another command does it, the flag cmd spells it as,
// where another noun's flag of that name sets the same field, and the flag it is
// a value of, when one of cmd's flags takes it. The hint wraps pflag's error, so
// goclikit still adds any flag spelled near it. Any other flag error comes back
// as it was.
func explainUnknownFlag(cmd *cobra.Command, err error) error {
	var missing *pflag.NotExistError
	if !errors.As(err, &missing) || missing.GetSpecifiedShortnames() != "" || missing.GetSpecifiedName() == "" {
		return err
	}
	typed := missing.GetSpecifiedName()
	var hints []string
	if command, ok := jobsOfAnotherCommand[cmd.CommandPath()][typed]; ok {
		hints = append(hints, fmt.Sprintf("--%s is not a flag of %s: %s", typed, cmd.CommandPath(), command))
	}
	if flag, ok := flagsSpelledOtherwise[cmd.CommandPath()][typed]; ok {
		hints = append(hints, fmt.Sprintf("--%s is not a flag of %s, which calls it --%s", typed, cmd.CommandPath(), flag))
	}
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		complete, ok := cmd.GetFlagCompletionFunc(flag.Name)
		if !ok || flag.Hidden {
			return
		}
		values, _ := complete(cmd, nil, "")
		if slices.Contains(values, typed) {
			hints = append(hints, fmt.Sprintf("%s is a value of --%s: %s --%s %s", typed, flag.Name, cmd.CommandPath(), flag.Name, typed))
		}
	})
	if len(hints) == 0 {
		return err
	}
	return fmt.Errorf("%w\n\n%s", err, strings.Join(hints, "\n"))
}
