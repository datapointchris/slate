package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

const writtenJSONHelp = "Output the record written, with its side effects and warnings, as JSON to stdout"

// reportWrite prints a write's answer. With asJSON it prints the whole answer,
// side effects and warnings included; otherwise the summary line, then each
// side effect indented under it. Warnings go to stderr either way, so a
// script reading the JSON still shows them to the person running it.
func reportWrite(cmd *cobra.Command, asJSON bool, written any, effects api.WriteEffects, summary string) error {
	for _, warning := range effects.Warnings {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Warning: "+warning.Message)
	}
	if asJSON {
		return encodeJSON(cmd.OutOrStdout(), written)
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), summary)
	for _, effect := range effects.SideEffects {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  "+effect.Message)
	}
	return nil
}
