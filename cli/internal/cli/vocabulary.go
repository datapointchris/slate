package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

func newVocabularyCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "vocabulary",
		Short: "List the values --status, --type, --priority and --label accept",
		Long: "Statuses, types and priorities are fixed by the API. Labels are made with\n" +
			"`slate labels create`, so they are the one list that grows.",
		Example: "  slate vocabulary\n  slate vocabulary --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			vocabulary, err := client.GetVocabulary(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), vocabulary)
			}
			printVocabulary(cmd.OutOrStdout(), vocabulary)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the vocabulary as JSON to stdout")
	return cmd
}

func printVocabulary(out io.Writer, v api.Vocabulary) {
	_, _ = fmt.Fprintf(out, "statuses:     %s\n", strings.Join(v.Statuses, ", "))
	_, _ = fmt.Fprintf(out, "types:        %s\n", strings.Join(v.Types, ", "))
	priorities := make([]string, 0, len(v.Priorities))
	for _, p := range v.Priorities {
		priorities = append(priorities, fmt.Sprintf("%d %s", p.Value, p.Name))
	}
	_, _ = fmt.Fprintf(out, "priorities:   %s\n", strings.Join(priorities, ", "))
	_, _ = fmt.Fprintf(out, "initiatives:  %s\n", strings.Join(v.InitiativeStatuses, ", "))
	_, _ = fmt.Fprintln(out, "\nlabels:")
	printIssueLabels(out, v.Labels)
}
