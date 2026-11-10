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
			"takes one scale. Statuses, types, priorities and steps are fixed by the API. Labels\n" +
			"are made with `slate labels create` and tags with `slate tags create`, so they\n" +
			"are the lists that grow. A --direction takes a direction `slate directions list`\n" +
			"prints, by number or exact name.",
		Example:     "  slate vocabulary\n  slate vocabulary --json",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsRead),
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
	_, _ = fmt.Fprintf(tw, "directions --status:\t%s\n", strings.Join(v.DirectionStatuses, ", "))
	_, _ = fmt.Fprintf(tw, "documents --kind:\t%s\n", strings.Join(v.DocumentKinds, ", "))
	_, _ = fmt.Fprintf(tw, "--priority:\t%s\n", strings.Join(priorities, ", "))
	_, _ = fmt.Fprintf(tw, "next --step:\t%s\n", strings.Join(v.QueueSteps, ", "))
	_ = tw.Flush()
	_, _ = fmt.Fprintln(out, "\nworklogs --step and claims hold --step, in the order a record goes through them:")
	printWorklogSteps(out, v.WorklogSteps)
	_, _ = fmt.Fprintln(out, "\nissues --label:")
	printIssueLabels(out, v.Labels)
	_, _ = fmt.Fprintln(out, "\nideas, initiatives, issues and next --tag:")
	printTags(out, v.Tags)
}

func printWorklogSteps(out io.Writer, steps []api.WorklogStep) {
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, step := range steps {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\n", step.Name, step.Description)
	}
	_ = tw.Flush()
}
