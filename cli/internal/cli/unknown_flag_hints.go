package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// flagElsewhereAnnotation prefixes the annotation naming where a flag cmd does
// not have is asked for instead.
const flagElsewhereAnnotation = "slate.flag-elsewhere."

// withFlagValues declares the closed set of values flag takes. The shell
// completes them, and an unknown flag spelled as one of them is answered with
// the flag that takes it: `--all` names `--status all`.
func withFlagValues(cmd *cobra.Command, flag string, values ...string) {
	if err := cmd.RegisterFlagCompletionFunc(flag, cobra.FixedCompletions(values, cobra.ShellCompDirectiveNoFileComp)); err != nil {
		panic(fmt.Sprintf("%s --%s: %v", cmd.CommandPath(), flag, err))
	}
}

// withFlagElsewhere answers `--flag` on cmd, which cmd does not have, with
// where it is asked instead.
func withFlagElsewhere(cmd *cobra.Command, flag, where string) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[flagElsewhereAnnotation+flag] = where
}

// hintedFlagError is an unknown flag with the hint that names what was meant.
// It does not unwrap to pflag's error, so goclikit adds no near-spelled flag
// beside it: `--api` is two edits from `--all` and means nothing like it.
type hintedFlagError struct {
	err  error
	hint string
}

func (e hintedFlagError) Error() string { return e.err.Error() + "\n\n" + e.hint }

// explainUnknownFlag names what an unknown long flag on cmd was meant as, when
// cmd knows: a flag asked for elsewhere, or a value one of its flags takes.
// Any other flag error comes back as it was.
func explainUnknownFlag(cmd *cobra.Command, err error) error {
	var missing *pflag.NotExistError
	if !errors.As(err, &missing) || missing.GetSpecifiedShortnames() != "" || missing.GetSpecifiedName() == "" {
		return err
	}
	typed := missing.GetSpecifiedName()
	if where := cmd.Annotations[flagElsewhereAnnotation+typed]; where != "" {
		return hintedFlagError{err: err, hint: where}
	}
	var hints []string
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
	return hintedFlagError{err: err, hint: strings.Join(hints, "\n")}
}
