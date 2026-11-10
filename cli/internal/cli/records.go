package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

// The ways to find a record after a 404, whatever its kind.
const (
	anyRecordHint = "Any record by its number, whatever its kind: slate show <number>"
	searchHint    = "Search every kind by number, title or description: slate search <words>"
)

// recordShown is `slate show <number> --json`: the kind, the number, and the
// JSON that kind's own show prints.
type recordShown struct {
	Kind   string `json:"kind"`
	Number int    `json:"number"`
	Record any    `json:"record"`
}

// worklogShown is `slate show <number>.<seq> --json`. A worklog is not a
// record, so its envelope names it by the name it is read back with, and
// record.record_number holds the record it sits under.
type worklogShown struct {
	Kind   string      `json:"kind"`
	Name   string      `json:"name"`
	Record api.Worklog `json:"record"`
}

func newShowCommand() *cobra.Command {
	var (
		asJSON      bool
		changeCount int
	)
	cmd := &cobra.Command{
		Use:        "show <number>",
		SuggestFor: []string{"get", "view", "record", "records"},
		Short:      "Show the idea, initiative, issue, direction or worklog a number names",
		Long: "Ideas, initiatives, issues and directions share one number sequence, so a\n" +
			"number names one record whatever its kind. A worklog is named under its\n" +
			"record's number, so 412.2 is the second worklog on #412. It prints what that\n" +
			"kind's own show prints: `slate show 412` and `slate issues show 412` read the\n" +
			"same. A record's show ends with its latest changes, newest first: each write\n" +
			"to the record and to the rows hanging off it.",
		Example:     "  slate show 412\n  slate show '#1501' --json\n  slate show 412.2",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			out, now := cmd.OutOrStdout(), time.Now()
			if name := strings.TrimPrefix(args[0], "#"); isWorklogName(name) {
				worklog, err := client.GetWorklog(cmd.Context(), name)
				if err != nil {
					return handleArgumentAPIError(err)
				}
				if asJSON {
					return encodeJSON(out, worklogShown{Kind: recordKindWorklog, Name: worklog.Name, Record: worklog})
				}
				printWorklogDetail(out, worklog, now)
				return nil
			}
			record, err := client.GetRecord(cmd.Context(), args[0], issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			changes, err := readChanges(cmd, client, record.Number, changeCount, asJSON)
			if err != nil {
				return err
			}
			defer noteChanges(cmd, changes)
			shown := recordShown{Kind: record.Kind, Number: record.Number}
			var print func()
			switch record.Kind {
			case api.RecordKindIdea:
				shown.Record = ideaShown{IdeaDetail: *record.Idea, Changes: changes}
				print = func() {
					printIdeaDetail(out, *record.Idea, now)
					printChanges(out, changes, now)
				}
			case api.RecordKindInitiative:
				initiative, err := readInitiativeIssues(cmd, client, *record.Initiative)
				if err != nil {
					return err
				}
				initiative.Changes = changes
				shown.Record = initiative
				print = func() { printInitiativeShown(cmd, initiative) }
			case api.RecordKindIssue:
				shown.Record = issueShown{IssueDetail: *record.Issue, Changes: changes}
				print = func() {
					printIssueDetail(out, *record.Issue, now)
					printChanges(out, changes, now)
				}
			case api.RecordKindDirection:
				direction := directionShown{DirectionDetail: *record.Direction, Changes: changes}
				shown.Record = direction
				print = func() { printDirectionShown(out, direction, now) }
			default:
				return fmt.Errorf("#%d is of kind %q, which slate show cannot print", record.Number, record.Kind)
			}
			if asJSON {
				return encodeJSON(out, shown)
			}
			print()
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the kind, the number and the record as JSON to stdout")
	addChangesFlag(cmd, &changeCount)
	return goclikit.WithRecoveryHints(cmd, searchHint, worklogHints[0])
}

// recordKindWorklog is the kind `slate show --json` names for a worklog, which
// is not a record but is named under one.
const recordKindWorklog = "worklog"

func isWorklogName(s string) bool {
	number, seq, found := strings.Cut(s, ".")
	return found && isItemNumber(number) && isItemNumber(seq)
}

func newSearchCommand() *cobra.Command {
	var (
		kind   string
		asJSON bool
		limit  int
	)
	cmd := &cobra.Command{
		Use:        "search <words>...",
		SuggestFor: []string{"find", "grep"},
		Short:      "Find ideas, initiatives, issues and directions by number, title or description",
		Long: "Every word must appear in the title, the description or an initiative's or\n" +
			"a direction's summary, in any order. A word that is a number, as 2175 or #2175, also matches\n" +
			"the record with that number, and it lists first. Every status is searched,\n" +
			"because the record you remember may be the one that closed. Unclosed records\n" +
			"come next, then the most urgent, then the newest.",
		Example: "  slate search rank renumber\n" +
			"  slate search 2175\n" +
			"  slate search \"device login\" --kind issue --json",
		Args:        cobra.MinimumNArgs(1),
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("kind") && !slices.Contains(api.RecordKinds, kind) {
				return goclikit.UsageError(fmt.Errorf("unknown kind %q — one of: %s", kind, strings.Join(api.RecordKinds, ", ")))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			filter := api.RecordFilter{Search: strings.Join(args, " "), Kind: kind}
			records, err := client.ListRecords(cmd.Context(), filter, limitFlag(cmd))
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), records)
			}
			printRecordsTable(cmd.OutOrStdout(), records)
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "Only records of this kind: "+strings.Join(api.RecordKinds, ", "))
	withFlagValues(cmd, "kind", api.RecordKinds...)
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output records as JSON to stdout")
	addLimitFlag(cmd, &limit)
	return cmd
}

func printRecordsTable(out io.Writer, records []api.RecordListed) {
	if len(records) == 0 {
		_, _ = fmt.Fprintln(out, "No record holds every word.")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NUMBER\tKIND\tSTATUS\tPRIORITY\tTITLE")
	for _, record := range records {
		priority := priorityName(record.Priority)
		if record.Kind == api.RecordKindDirection {
			priority = "-"
		}
		_, _ = fmt.Fprintf(tw, "#%d\t%s\t%s\t%s\t%s\n", record.Number, record.Kind, orDash(strValue(record.Status)), priority, record.Title)
	}
	_ = tw.Flush()
}
