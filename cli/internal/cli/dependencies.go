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
			"every unclosed issue in the waiting initiative is blocked. An initiative is\n" +
			"open while active or someday.",
		Example: "  slate dependencies add 412 --depends-on 411    412 cannot start until 411 closes\n" +
			"  slate dependencies add 2794 --depends-on 2653  initiative 2794 starts once 2653 closes\n" +
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
		Example:     "  slate dependencies add 412 --depends-on 411\n  slate dependencies add 2794 --depends-on 2653",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeRecordsRead, scopeRecordsPlan),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dependsOn == "" {
				return goclikit.UsageError(errors.New("--depends-on is required"))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			kind, err := waiterKind(cmd, client, args[0])
			if err != nil {
				return err
			}
			if kind == api.RecordKindInitiative {
				written, err := client.AddInitiativeDependency(cmd.Context(), args[0], dependsOn)
				if err != nil {
					return handleArgumentAPIError(err)
				}
				initiative := written.Initiative
				summary := fmt.Sprintf("#%d %s now waits on %s", initiative.Number, initiative.Name, strings.Join(waitedOnNumbers(initiative.DependsOn), ", "))
				return reportWrite(cmd, asJSON, written, written.WriteEffects, summary)
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
		Example:     "  slate dependencies remove 412 --depends-on 411\n  slate dependencies remove 2794 --depends-on 2653",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeRecordsRead, scopeRecordsPlan),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dependsOn == "" {
				return goclikit.UsageError(errors.New("--depends-on is required"))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			kind, err := waiterKind(cmd, client, args[0])
			if err != nil {
				return err
			}
			if kind == api.RecordKindInitiative {
				written, err := client.RemoveInitiativeDependency(cmd.Context(), args[0], dependsOn)
				if err != nil {
					return handleArgumentAPIError(err)
				}
				initiative := written.Initiative
				summary := fmt.Sprintf("#%d %s no longer waits on %s", initiative.Number, initiative.Name, issueRefLabel(dependsOn))
				if remaining := waitedOnNumbers(initiative.DependsOn); len(remaining) > 0 {
					summary += ", and still waits on " + strings.Join(remaining, ", ")
				}
				return reportWrite(cmd, asJSON, written, written.WriteEffects, summary)
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
	cmd.Flags().StringVar(&dependsOn, "depends-on", "", "Record it no longer waits on (required)")
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

func waitedOnNumbers(summaries []api.RecordSummary) []string {
	numbers := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		numbers = append(numbers, "#"+strconv.Itoa(summary.Number))
	}
	return numbers
}

// waiterKind is the kind of record a dependency's first argument names, which
// decides the route its edge is written on. A number is read from the API. A
// UUID is read as an issue's, and anything else as an initiative's name, since
// only an initiative has one.
func waiterKind(cmd *cobra.Command, client *api.Client, ref string) (string, error) {
	switch {
	case isItemNumber(strings.TrimPrefix(ref, "#")):
		record, err := client.GetRecord(cmd.Context(), strings.TrimPrefix(ref, "#"), issueZone())
		if err != nil {
			return "", handleArgumentAPIError(err)
		}
		if record.Kind == api.RecordKindIdea {
			return "", fmt.Errorf("#%d is an idea, and only an issue or an initiative waits on a record — promote it first", record.Number)
		}
		return record.Kind, nil
	case looksLikeUUID(ref):
		return api.RecordKindIssue, nil
	default:
		return api.RecordKindInitiative, nil
	}
}
