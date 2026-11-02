package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

func newDependenciesCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:        "dependencies",
		SuggestFor: []string{"deps", "dependency"},
		Short:      "Record what an issue waits on, and draw the trees the edges make",
		Long: "An issue that depends on another cannot start until that one closes, so it\n" +
			"is not ready while anything it depends on is unclosed. The issue it waits on\n" +
			"takes its priority when that is more urgent.",
	})
	goclikit.WithRecoveryHints(cmd, issueHints...)
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupReading,
		goclikit.WithRecoveryHints(newDependenciesTreeCommand(), issueHints...),
	)...)
	cmd.AddCommand(inGroup(groupChanging,
		goclikit.WithRecoveryHints(newDependenciesAddCommand(), issueHints...),
		goclikit.WithRecoveryHints(newDependenciesRemoveCommand(), issueHints...),
	)...)
	return cmd
}

func newDependenciesAddCommand() *cobra.Command {
	var (
		dependsOn string
		asJSON    bool
	)
	cmd := &cobra.Command{
		Use:   "add <issue> --depends-on <other-issue>",
		Short: "Record that an issue waits on another",
		Long: "An edge that would leave an issue waiting on itself, through dependencies or\n" +
			"a parent, is refused, and the refusal prints the path.",
		Example: "  slate dependencies add 412 --depends-on 411",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dependsOn == "" {
				return goclikit.UsageError(errors.New("--depends-on is required"))
			}
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.AddIssueDependency(cmd.Context(), args[0], dependsOn, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			issue := written.Issue
			summary := fmt.Sprintf("#%d %s now waits on %s", issue.Number, issue.Title, strings.Join(dependencyNumbers(issue.DependsOn), ", "))
			return reportWrite(cmd, asJSON, written, written.WriteEffects, summary)
		},
	}
	cmd.Flags().StringVar(&dependsOn, "depends-on", "", "Issue it waits on (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newDependenciesRemoveCommand() *cobra.Command {
	var (
		dependsOn string
		asJSON    bool
	)
	cmd := &cobra.Command{
		Use:   "remove <issue> --depends-on <other-issue>",
		Short: "Remove the edge between an issue and one it waits on",
		Long: "Adding the edge back restores it, so this asks nothing first. The issue it\n" +
			"leaves is printed with what it still waits on.",
		Example: "  slate dependencies remove 412 --depends-on 411",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dependsOn == "" {
				return goclikit.UsageError(errors.New("--depends-on is required"))
			}
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.RemoveIssueDependency(cmd.Context(), args[0], dependsOn, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			issue := written.Issue
			summary := fmt.Sprintf("#%d %s no longer waits on %s", issue.Number, issue.Title, issueRefLabel(dependsOn))
			if remaining := dependencyNumbers(issue.DependsOn); len(remaining) > 0 {
				summary += ", and still waits on " + strings.Join(remaining, ", ")
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, summary)
		},
	}
	cmd.Flags().StringVar(&dependsOn, "depends-on", "", "Issue it no longer waits on (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func dependencyNumbers(summaries []api.IssueSummary) []string {
	numbers := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		numbers = append(numbers, "#"+strconv.Itoa(summary.Number))
	}
	return numbers
}
