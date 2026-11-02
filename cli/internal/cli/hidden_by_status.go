package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// noteHiddenByStatus says on stderr which rows the default --status left out,
// and the command that includes them. It prints under --json too, where stdout
// is the data and a reader would not otherwise know the rows were held back.
func noteHiddenByStatus(cmd *cobra.Command, hidden string) {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\n%s are hidden: %s\n", hidden, reinvocation(cmd, "--status all"))
}

// reinvocation is the command that was run, flags and all, with extra added: a
// hint that widens a filtered read must keep the filters it widens.
func reinvocation(cmd *cobra.Command, extra string) string {
	parts := []string{cmd.CommandPath()}
	for _, arg := range cmd.Flags().Args() {
		parts = append(parts, shellQuote(arg))
	}
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		if flag.Name == "json" {
			return
		}
		if flag.Value.Type() == "bool" {
			if flag.Value.String() == "true" {
				parts = append(parts, "--"+flag.Name)
			} else {
				parts = append(parts, "--"+flag.Name+"=false")
			}
			return
		}
		for _, value := range rawFlagValues(flag) {
			parts = append(parts, "--"+flag.Name, shellQuote(value))
		}
	})
	return strings.Join(append(parts, extra), " ")
}
