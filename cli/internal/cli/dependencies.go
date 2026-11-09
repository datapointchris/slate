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
		Short:      "Record what an issue or an initiative waits on, and draw the trees the edges make",
		Long: "An issue that depends on another cannot start until that one closes, so it\n" +
			"is not ready while anything it depends on is unclosed. The issue it waits on\n" +
			"takes its priority when that is more urgent.\n" +
			"\n" +
			"An initiative waits on an initiative or an issue. While that record is open,\n" +
			"every unclosed issue in the waiting initiative is blocked. A someday\n" +
			"initiative counts as open.",
		Example: "  slate dependencies add 412 --depends-on 411    412 cannot start until 411 closes\n" +
			"  slate dependencies add 520 --depends-on 498    initiative 520 starts once 498 closes\n" +
			"  slate dependencies tree 412                    the tree 412 sits in\n" +
			"  slate dependencies tree 412 --invert           what waits on 412\n" +
			"  slate dependencies tree                        every tree with work left in it",
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
		Use:   "add <issue or initiative> --depends-on <record>",
		Short: "Record that an issue or an initiative waits on another record",
		Long: "An issue waits on an issue. An initiative waits on an initiative or an issue,\n" +
			"and every unclosed issue in it is blocked while that record is open.\n" +
			"\n" +
			"An edge that would leave a record waiting on itself is refused, and the\n" +
			"refusal prints the loop. A loop may run through dependencies, parents and\n" +
			"initiatives' waits. An initiative waits on its unclosed issues, and each of\n" +
			"them on what the initiative waits on, so an initiative waiting on one of its\n" +
			"own issues is such a loop.",
		Example:     "  slate dependencies add 412 --depends-on 411\n  slate dependencies add 520 --depends-on 498",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeRecordsPlan),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dependsOn == "" {
				return goclikit.UsageError(errors.New("--depends-on is required"))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.AddDependency(cmd.Context(), args[0], dependsOn, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			number, name, waitsOn := dependencyWaiter(written)
			return reportWrite(cmd, asJSON, written, written.WriteEffects, fmt.Sprintf("#%d %s now waits on %s", number, name, strings.Join(waitsOn, ", ")))
		},
	}
	cmd.Flags().StringVar(&dependsOn, "depends-on", "", "Record it waits on: an issue, or for an initiative an initiative or an issue (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newDependenciesRemoveCommand() *cobra.Command {
	var (
		dependsOn string
		asJSON    bool
	)
	cmd := &cobra.Command{
		Use:   "remove <issue or initiative> --depends-on <record>",
		Short: "Remove the edge between an issue or an initiative and a record it waits on",
		Long: "Adding the edge back restores it, so this asks nothing first. The record it\n" +
			"leaves is printed with what it still waits on.",
		Example:     "  slate dependencies remove 412 --depends-on 411\n  slate dependencies remove 520 --depends-on 498",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeRecordsPlan),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dependsOn == "" {
				return goclikit.UsageError(errors.New("--depends-on is required"))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.RemoveDependency(cmd.Context(), args[0], dependsOn, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			number, name, waitsOn := dependencyWaiter(written)
			summary := fmt.Sprintf("#%d %s no longer waits on %s", number, name, issueRefLabel(dependsOn))
			if len(waitsOn) > 0 {
				summary += ", and still waits on " + strings.Join(waitsOn, ", ")
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, summary)
		},
	}
	cmd.Flags().StringVar(&dependsOn, "depends-on", "", "Record it no longer waits on (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

// dependencyWaiter is the number and title of the issue or initiative an edge
// was written from, and what it waits on now.
func dependencyWaiter(written api.DependencyWritten) (int, string, []string) {
	if initiative := written.Initiative; initiative != nil {
		return initiative.Number, initiative.Name, summaryNumbers(initiative.DependsOn)
	}
	if issue := written.Issue; issue != nil {
		numbers := make([]string, 0, len(issue.DependsOn))
		for _, dependency := range issue.DependsOn {
			numbers = append(numbers, "#"+strconv.Itoa(dependency.Number))
		}
		return issue.Number, issue.Title, numbers
	}
	return 0, "", nil
}

func summaryNumbers(summaries []api.RecordSummary) []string {
	numbers := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		numbers = append(numbers, "#"+strconv.Itoa(summary.Number))
	}
	return numbers
}
