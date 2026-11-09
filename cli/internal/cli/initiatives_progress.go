package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/gotermboard"
	"github.com/datapointchris/gotermstyle"
	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

// recentCloses is how far back progress lists closes when --since is not given.
// The API itself leaves an absent bound open, as every windowed read does.
const recentCloses = "7d"

// progressBarCells is how many cells progress's bar spans, wide enough to show
// the work under way and blocked beside what is done.
const progressBarCells = 30

func newInitiativesProgressCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "progress [<initiative>...]",
		Short: "Show each initiative's bar, its issues by state, what runs, waits, is blocked and comes next, its closes and its tokens",
		Long: "For each initiative named, or every active one in list order when none is:\n" +
			"a bar of its done issues over its done and open ones, with the work under way\n" +
			"in yellow and the blocked work in red; every issue counted in one state; the\n" +
			"issues running, waiting and blocked; the next few in queue order; the issues\n" +
			"closed inside --since and --until; and the worklogs, sessions, links and\n" +
			"tokens by kind of its whole life.\n\n" +
			"An issue under a live claim counts as running, or as waiting where its holder\n" +
			"parked it, whatever else holds it back. Given neither --since nor --until, the\n" +
			"closes are those since --since's default. --since= lists every close. --json\n" +
			"prints an array, one entry per initiative, carrying every figure the text shows.",
		Example: "  slate initiatives progress\n" +
			"  slate initiatives progress 2072 \"Ship the tracker\"\n" +
			"  slate initiatives progress 2072 --since 2026-10-26 --json",
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The recent default stands in for a window only when none is given, so
			// --until alone runs from the first close, as on every windowed read.
			if !cmd.Flags().Changed("since") && !cmd.Flags().Changed("until") {
				_ = cmd.Flags().Set("since", recentCloses)
			}
			window, err := readWindow(cmd)
			if err != nil {
				return err
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			all, err := client.ListInitiativesProgress(cmd.Context(), args, window, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), all)
			}
			printInitiativesProgress(cmd.OutOrStdout(), all, time.Now())
			return nil
		},
	}
	addWindowFlags(cmd, "issues closed")
	cmd.Flags().Lookup("since").DefValue = recentCloses
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output each initiative's progress as a JSON array to stdout")
	return goclikit.WithRecoveryHints(cmd, initiativeHints...)
}

func printInitiativesProgress(out io.Writer, all []api.InitiativeProgress, now time.Time) {
	if len(all) == 0 {
		_, _ = fmt.Fprintln(out, "No active initiatives.")
		return
	}
	view := progressView{out: out, p: gotermstyle.PaletteFor(out), clip: clipsToTerminal(out), now: now}
	for i, progress := range all {
		if i > 0 {
			_, _ = fmt.Fprintln(out)
		}
		view.print(progress)
	}
}

// progressView prints one initiative's progress at a time to out.
type progressView struct {
	out  io.Writer
	p    gotermstyle.Palette
	clip bool
	now  time.Time
}

func (v progressView) line(format string, args ...any) {
	_, _ = fmt.Fprintf(v.out, format+"\n", args...)
}

// fit clips text that starts at column used so it ends at the terminal's edge.
// Piped output is read whole, so it is never clipped.
func (v progressView) fit(text string, used int) string {
	if !v.clip {
		return text
	}
	return gotermstyle.Clip(text, used)
}

func (v progressView) print(progress api.InitiativeProgress) {
	p, initiative, states := v.p, progress.Initiative, progress.States
	number, label, color := "#"+strconv.Itoa(initiative.Number), initiativeGroupLabel(initiative), tierColor(p, initiative.Priority)
	if initiative.Status != "active" {
		color = p.Magenta
	}
	used := utf8.RuneCountInString(number) + 2 + utf8.RuneCountInString(label) + 2
	v.line("%s  %s  %s", p.Cyan(number), color(label), p.Bold(v.fit(initiative.Name, used)))

	done, open := initiative.CompletedCount, initiative.OpenCount
	v.line("  %s  %d/%d done", progressBar(p, states, done, open), done, done+open)
	v.line("  %s", stateCountsLine(p, states, initiative.IssueCount))
	if initiative.Claim != nil {
		v.line("  %s", v.fit(claimNote(*initiative.Claim, v.now), 2))
	}
	for _, waited := range initiative.DependsOn {
		v.line("  waits on %s", v.fit(recordSummaryLine(waited), 11))
	}
	for _, waiting := range initiative.WaitingInitiatives {
		v.line("  waited on by %s", v.fit(recordSummaryLine(waiting), 15))
	}

	v.claimedSection("Running", progress.Running)
	v.claimedSection("Waiting", progress.Waiting)
	v.nextSection(progress.Next)
	v.blockedSection(progress.Blocked)
	v.closedSection(progress)

	totals := progress.Totals
	v.line("")
	v.line("  work:    %s %s · %s %s · %s %s · %s", groupedDigits(totals.WorklogCount), plural(totals.WorklogCount, "worklog", "worklogs"),
		groupedDigits(totals.SessionCount), plural(totals.SessionCount, "session", "sessions"),
		groupedDigits(totals.MessageCount), plural(totals.MessageCount, "message", "messages"), linkCountsLine(totals.LinkCounts))
	v.line("  tokens:  %s", reportedTokens(totals.Tokens))
}

// progressBar fills its done cells by the rule the list's bar keeps, then shares the
// empty cells among the open kinds. It draws them as slate tui's bar does, so the two
// read alike with or without color.
func progressBar(p gotermstyle.Palette, s api.IssueStateCounts, done, open int) string {
	filled := progressFilled(done, open, progressBarCells)
	empty := progressBarCells - filled
	share := func(n int) int {
		if open == 0 {
			return 0
		}
		return (n*empty + open/2) / open
	}
	underWay, blocked, _ := openKinds(s, open)
	underWayCells := share(underWay)
	blockedCells := share(underWay+blocked) - underWayCells
	return gotermboard.TallyBar(filled, underWayCells, blockedCells, empty-underWayCells-blockedCells).Paint(p)
}

// stateCountsLine names the done count always, every other state holding an
// issue, and the total, done in green, the work under way in yellow and the
// blocked work in red.
func stateCountsLine(p gotermstyle.Palette, s api.IssueStateCounts, total int) string {
	parts := make([]string, 0, 11)
	for _, count := range stateCounts(s) {
		parts = append(parts, p.Paint(count.code, strconv.Itoa(count.n)+" "+count.name))
	}
	return strings.Join(append(parts, strconv.Itoa(total)+" "+plural(total, "issue", "issues")), " · ")
}

// stateCount is one state's issues, its name and its color.
type stateCount struct {
	n    int
	name string
	code string
}

// stateCounts is the done count always, then every other state holding an issue.
func stateCounts(s api.IssueStateCounts) []stateCount {
	counts := []stateCount{{s.Completed, "done", gotermstyle.CodeGreen}}
	for _, state := range []stateCount{
		{s.Running, "running", gotermstyle.CodeYellow},
		{s.Waiting, "waiting", gotermstyle.CodeYellow},
		{s.Blocked, "blocked", gotermstyle.CodeRed},
		{s.Ready, "ready", ""},
		{s.Triage, "in triage", ""},
		{s.Deferred, "deferred", ""},
		{s.Shelved, "shelved", ""},
		{s.OpenChildren, "with open children", ""},
		{s.Canceled, "canceled", ""},
	} {
		if state.n > 0 {
			counts = append(counts, state)
		}
	}
	return counts
}

// progressRow is one issue or record under a section heading. Cells sit
// between its number and its title, padded across the section, and note goes
// on its own line under the title.
type progressRow struct {
	number int
	cells  []string
	colors []func(string) string
	title  string
	note   string
}

func (v progressView) section(heading string, rows []progressRow) {
	if len(rows) == 0 {
		return
	}
	v.line("")
	v.line("  %s", v.p.Bold(fmt.Sprintf("%s · %d", heading, len(rows))))
	numbers := make([]string, len(rows))
	for i, row := range rows {
		numbers[i] = "#" + strconv.Itoa(row.number)
	}
	numberWidth := widest(numbers)
	var widths []int
	for _, row := range rows {
		for c, cell := range row.cells {
			if c == len(widths) {
				widths = append(widths, 0)
			}
			widths[c] = max(widths[c], utf8.RuneCountInString(cell))
		}
	}
	for i, row := range rows {
		cells := []string{"   ", paint(v.p.Cyan, numbers[i], numberWidth)}
		used := 3 + 2 + numberWidth
		for c, cell := range row.cells {
			cells = append(cells, paint(row.colors[c], cell, widths[c]))
			used += 2 + widths[c]
		}
		v.line("%s", strings.Join(append(cells, v.fit(row.title, used+2)), "  "))
		if row.note != "" {
			indent := 3 + 2 + numberWidth + 2
			v.line("%s%s", strings.Repeat(" ", indent), v.fit(row.note, indent))
		}
	}
}

// claimedSection lists issues under a live claim, each with its holder, its
// expiry, what its holder waits on, and the unclosed issues it still depends on.
func (v progressView) claimedSection(heading string, issues []api.Issue) {
	rows := make([]progressRow, 0, len(issues))
	for _, issue := range issues {
		var notes []string
		if issue.Claim != nil {
			notes = append(notes, claimNote(*issue.Claim, v.now))
		}
		if held := blockers(issue); held != "" {
			notes = append(notes, "blocked by "+held)
		}
		rows = append(rows, progressRow{number: issue.Number, title: issue.Title, note: strings.Join(notes, "; ")})
	}
	v.section(heading, rows)
}

// nextSection lists the head of the queue, each row with the step it asks
// for and the priority it sorts at.
func (v progressView) nextSection(next []api.QueueRow) {
	rows := make([]progressRow, 0, len(next))
	plain := func(text string) string { return text }
	for _, row := range next {
		priority := priorityName(row.Priority)
		if row.Issue != nil {
			priority = priorityCell(*row.Issue)
		}
		rows = append(rows, progressRow{
			number: row.Number,
			cells:  []string{row.Step, priority},
			colors: []func(string) string{plain, tierColor(v.p, row.Priority)},
			title:  row.Title,
		})
	}
	v.section("Next", rows)
}

// blockedSection lists the blocked issues, each with the unclosed issues it
// waits on and the open records its initiative waits on.
func (v progressView) blockedSection(issues []api.Issue) {
	rows := make([]progressRow, 0, len(issues))
	for _, issue := range issues {
		note := "waits on " + blockers(issue)
		rows = append(rows, progressRow{number: issue.Number, title: issue.Title, note: note})
	}
	v.section("Blocked", rows)
}

// closedSection lists the closes inside the window, latest first, under a
// heading naming the window.
func (v progressView) closedSection(progress api.InitiativeProgress) {
	heading := "Closed"
	switch since, until := progress.ClosedSince, progress.ClosedUntil; {
	case since != nil && until != nil:
		heading += " " + localClock(*since, v.now) + " to " + localClock(*until, v.now)
	case since != nil:
		heading += " since " + localClock(*since, v.now)
	case until != nil:
		heading += " before " + localClock(*until, v.now)
	}
	rows := make([]progressRow, 0, len(progress.Closed))
	plain := func(text string) string { return text }
	for _, issue := range progress.Closed {
		closed := "—"
		if issue.ClosedTS != nil {
			closed = localClock(*issue.ClosedTS, v.now)
		}
		statusColor := plain
		if issue.Status == api.IssueStatusCompleted {
			statusColor = v.p.Green
		}
		rows = append(rows, progressRow{
			number: issue.Number,
			cells:  []string{issue.Status, closed},
			colors: []func(string) string{statusColor, plain},
			title:  issue.Title,
		})
	}
	v.section(heading, rows)
}
