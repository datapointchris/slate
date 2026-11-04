package cli

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/gotermstyle"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/datapointchris/slate/cli/internal/api"
)

// defaultNextLimit caps `next`. The head of the queue is the answer, and the
// rows after it are context.
const defaultNextLimit = 10

func newNextCommand() *cobra.Command {
	var (
		asJSON    bool
		queue     readyQueueFlags
		step      string
		limit     int
		forRecord int
	)
	cmd := &cobra.Command{
		Use:   "next",
		Short: "Every kind of open work, in the order to take it",
		Long: "Ideas, initiatives and issues by priority, each row with its step: discuss an\n" +
			"idea, plan an initiative with no open issue in it, decide a decision issue,\n" +
			"build any other ready issue. --step keeps one step's rows. Within a priority,\n" +
			"each initiative's issues run together in the order their ranks set, and\n" +
			"`slate initiatives reorder` moves an initiative with its issues.\n" +
			"\n" +
			"An issue is ready when it is open, unblocked, not deferred past today, and has\n" +
			"no open children. Work a live claim holds is listed apart under Claimed, by\n" +
			"holder. A row naming paths that a held issue on its repo also changes says\n" +
			"so, so the collision shows before anyone takes it.\n" +
			"\n" +
			"--repo, --type, --label and --autofixable read only issues, so each leaves the\n" +
			"ideas and initiatives out. `slate claims hold --next` holds the head of\n" +
			"`slate next --step build` under the same flags. Reading the queue holds\n" +
			"nothing, so hold what you take before working it.\n" +
			"\n" +
			"The priority column is the one the queue sorts by, and a * marks one inherited\n" +
			"from what the issue blocks, its parent, or its initiative. A pin sorts ahead of\n" +
			"every priority, and a row it put there says `pinned`: the pinned record, its\n" +
			"initiative's issues, and what they wait on.\n" +
			"\n" +
			"--for reads the plan of one record: its own open work and every open issue it\n" +
			"transitively waits on, in any initiative or repo, ready or not, in queue order.\n" +
			"Each row says which of them it unblocks, and every row shows unless -n caps it.",
		Example: "  slate next\n" +
			"  slate next --for 1544\n" +
			"  slate next --step build --limit 1\n" +
			"  slate next --step build --repo webapp --json\n" +
			"  slate next --step decide\n" +
			"  slate next --step build --autofixable --json",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter, err := queue.filter(cmd)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("step") && !slices.Contains(api.QueueSteps, step) {
				return goclikit.UsageError(fmt.Errorf("unknown step %q — one of: %s", step, strings.Join(api.QueueSteps, ", ")))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			read, err := client.GetQueue(cmd.Context(), api.QueueFilter{ReadyFilter: filter, Step: step, For: forRecord}, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if cmd.Flags().Changed("for") && !cmd.Flags().Changed("limit") {
				limit = len(read.Queue)
			}
			shown := read.Queue[:min(limit, len(read.Queue))]
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), api.Queue{Queue: shown, Claimed: read.Claimed})
			}
			out, now := cmd.OutOrStdout(), time.Now()
			if len(read.Queue) == 0 {
				_, _ = fmt.Fprintln(out, "Nothing is queued.")
			} else if len(shown) > 0 {
				printQueueTable(out, shown, false, now)
			}
			if len(read.Claimed) > 0 {
				_, _ = fmt.Fprintln(out, "\nClaimed:")
				printQueueTable(out, read.Claimed, true, now)
			}
			noteInheritedPriority(cmd, queuedIssues(slices.Concat(shown, read.Claimed)))
			noteCappedRows(cmd, len(shown), len(read.Queue))
			return nil
		},
	}
	queue.register(cmd)
	cmd.Flags().StringVar(&step, "step", "", "Only rows at this step: "+strings.Join(api.QueueSteps, ", "))
	withFlagValues(cmd, "step", api.QueueSteps...)
	cmd.Flags().IntVar(&forRecord, "for", 0, "Only this record's open work and every open issue it transitively waits on")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the queue and the claimed work as JSON to stdout")
	addCapFlag(cmd, &limit, defaultNextLimit, "Maximum number of queued records to show; claimed work is always shown")
	return cmd
}

// printQueueTable prints queue rows of every kind. held adds the HOLDER column
// the claimed rows are grouped by.
func printQueueTable(out io.Writer, rows []api.QueueRow, held bool, now time.Time) {
	p := gotermstyle.PaletteFor(out)
	header := []string{"#", "KIND", "STEP", "PRIORITY", "INITIATIVE", "REPO"}
	if held {
		header = slices.Insert(header, 1, "HOLDER")
	}
	cells := make([][]string, len(rows))
	for i, row := range rows {
		cells[i] = queueRowCells(row)
		if held {
			cells[i] = slices.Insert(cells[i], 1, strValue(row.Holder))
		}
	}
	widths := make([]int, len(header))
	for c := range header {
		column := []string{header[c]}
		for _, row := range cells {
			column = append(column, row[c])
		}
		widths[c] = widest(column)
	}

	line := make([]string, 0, len(header)+1)
	for c, title := range header {
		line = append(line, paint(p.Bold, title, widths[c]))
	}
	_, _ = fmt.Fprintln(out, strings.Join(append(line, p.Bold("TITLE")), "  "))
	priorityColumn := slices.Index(header, "PRIORITY")
	for i, row := range rows {
		line = line[:0]
		for c, cell := range cells[i] {
			color := func(text string) string { return text }
			switch c {
			case 0:
				color = p.Cyan
			case priorityColumn:
				color = tierColor(p, row.Priority)
			}
			line = append(line, paint(color, cell, widths[c]))
		}
		title := row.Title
		if notes := queueRowNotes(row, now); len(notes) > 0 {
			title += "  [" + strings.Join(notes, "; ") + "]"
		}
		_, _ = fmt.Fprintln(out, strings.Join(append(line, title), "  "))
	}
}

// queueRowCells are a row's #, KIND, STEP, PRIORITY, INITIATIVE and REPO.
func queueRowCells(row api.QueueRow) []string {
	priority, initiative, repo := priorityName(row.Priority), "—", "—"
	switch {
	case row.Issue != nil:
		priority = priorityCell(*row.Issue)
		if row.Issue.Initiative != nil {
			initiative = strconv.Itoa(row.Issue.Initiative.Number)
		}
		repo = orDash(strValue(row.Issue.Repo))
	case row.Initiative != nil:
		repo = orDash(strings.Join(row.Initiative.Repos, ", "))
	}
	return []string{strconv.Itoa(row.Number), row.Kind, row.Step, priority, initiative, repo}
}

// queueRowNotes says what the reader acts on before taking a row: its claim,
// what keeps a held issue from being ready, and each held issue changing the
// same code.
func queueRowNotes(row api.QueueRow, now time.Time) []string {
	var notes []string
	switch {
	case row.Issue != nil:
		notes = issueStateNotes(*row.Issue, now)
	case row.Initiative != nil:
		if row.Initiative.Claim != nil {
			notes = append(notes, claimNote(*row.Initiative.Claim, now))
		}
		if open := row.Initiative.OpenCount; open > 0 {
			notes = append(notes, fmt.Sprintf("%d open %s", open, plural(open, "issue", "issues")))
		}
	case row.Idea != nil:
		if row.Idea.Claim != nil {
			notes = append(notes, claimNote(*row.Idea.Claim, now))
		}
	}
	for _, overlap := range row.Overlaps {
		notes = append(notes, fmt.Sprintf("overlaps #%d held by %s on %s", overlap.Number, overlap.Holder, strings.Join(overlap.Paths, ", ")))
	}
	if len(row.Unblocks) > 0 {
		waiting := make([]string, len(row.Unblocks))
		for i, number := range row.Unblocks {
			waiting[i] = "#" + strconv.Itoa(number)
		}
		notes = append(notes, "unblocks "+strings.Join(waiting, ", "))
	}
	if row.Pinned {
		notes = append([]string{"pinned"}, notes...)
	}
	return notes
}

func queuedIssues(rows []api.QueueRow) []api.Issue {
	var issues []api.Issue
	for _, row := range rows {
		if row.Issue != nil {
			issues = append(issues, *row.Issue)
		}
	}
	return issues
}

// readyQueueFlags narrow the ready issues, on `next` and `claims hold --next`.
type readyQueueFlags struct {
	repo        string
	issueType   string
	label       string
	initiative  string
	autofixable bool
}

// readyQueueFlagNames are refused by a hold that names its record.
func readyQueueFlagNames() []string {
	probe := &cobra.Command{}
	new(readyQueueFlags).register(probe)
	var names []string
	probe.Flags().VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	return names
}

func (v *readyQueueFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&v.repo, "repo", "", "Only issues on this repo (empty string for issues on no repo)")
	f.StringVar(&v.issueType, "type", "", "Only issues of this type: "+strings.Join(api.IssueTypes, ", "))
	withFlagValues(cmd, "type", api.IssueTypes...)
	f.StringVar(&v.label, "label", "", "Only issues carrying this label")
	f.StringVar(&v.initiative, "initiative", "", "Only work in this initiative, by number, name or id")
	f.BoolVar(&v.autofixable, "autofixable", false, autofixableFilterHelp)
}

func (v *readyQueueFlags) filter(cmd *cobra.Command) (api.ReadyFilter, error) {
	if err := validateIssueType(cmd.Flags(), "type", v.issueType); err != nil {
		return api.ReadyFilter{}, err
	}
	repo, err := repoFlagValue(cmd, v.repo)
	if err != nil {
		return api.ReadyFilter{}, err
	}
	filter := api.ReadyFilter{
		Repo:       repo,
		Type:       v.issueType,
		Label:      v.label,
		Initiative: v.initiative,
	}
	if cmd.Flags().Changed("autofixable") {
		filter.Autofixable = &v.autofixable
	}
	return filter, nil
}
