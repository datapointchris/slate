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

// explainUnknownFlag names the flag an unknown long flag on cmd is a value of,
// when one of cmd's flags takes it. The hint wraps pflag's error, so goclikit
// still adds any flag spelled near it. Any other flag error comes back as it was.
func explainUnknownFlag(cmd *cobra.Command, err error) error {
	var missing *pflag.NotExistError
	if !errors.As(err, &missing) || missing.GetSpecifiedShortnames() != "" || missing.GetSpecifiedName() == "" {
		return err
	}
	typed := missing.GetSpecifiedName()
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
	return fmt.Errorf("%w\n\n%s", err, strings.Join(hints, "\n"))
}
