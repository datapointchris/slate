package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

func newVocabularyCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "vocabulary",
		Short: "List the values each closed flag accepts, by the command that takes it",
		Long: "Each list is named for the command whose flag takes it: `issues --status`\n" +
			"and `initiatives --status` accept different values. Every noun's --priority\n" +
			"takes one scale. Statuses, types and priorities are fixed by the API. Labels\n" +
			"are made with `slate labels create`, so they are the one list that grows.",
		Example:     "  slate vocabulary\n  slate vocabulary --json",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsPlan),
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAPIClient(cmd)
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
	priorities := make([]string, 0, len(v.Priorities))
	for _, p := range v.Priorities {
		priorities = append(priorities, fmt.Sprintf("%d %s", p.Value, p.Name))
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "issues --status:\t%s\n", strings.Join(v.IssueStatuses, ", "))
	_, _ = fmt.Fprintf(tw, "issues --type:\t%s\n", strings.Join(v.IssueTypes, ", "))
	_, _ = fmt.Fprintf(tw, "initiatives --status:\t%s\n", strings.Join(v.InitiativeStatuses, ", "))
	_, _ = fmt.Fprintf(tw, "documents --kind:\t%s\n", strings.Join(v.DocumentKinds, ", "))
	_, _ = fmt.Fprintf(tw, "worklogs --workflow:\t%s\n", strings.Join(v.WorklogWorkflows, ", "))
	_, _ = fmt.Fprintf(tw, "--priority:\t%s\n", strings.Join(priorities, ", "))
	_ = tw.Flush()
	_, _ = fmt.Fprintln(out, "\nissues --label:")
	printIssueLabels(out, v.Labels)
}
