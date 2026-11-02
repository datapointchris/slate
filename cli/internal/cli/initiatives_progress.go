package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/datapointchris/goclikit"
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
			"parked it, whatever else holds it back. --since defaults to 7 days back, and\n" +
			"--since= lists every close. --json prints an array, one entry per initiative,\n" +
			"carrying every figure the text shows.",
		Example: "  slate initiatives progress\n" +
			"  slate initiatives progress 2072 \"Ship the tracker\"\n" +
			"  slate initiatives progress 2072 --since 2026-10-26 --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			window, err := readWindow(cmd)
			if err != nil {
				return err
			}
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			refs, named := args, len(args) > 0
			if !named {
				active, err := client.ListInitiatives(cmd.Context(), "", "", nil)
				if err != nil {
					return handleAPIError(err)
				}
				for _, initiative := range active {
					refs = append(refs, strconv.Itoa(initiative.Number))
				}
			}
			all := make([]api.InitiativeProgress, 0, len(refs))
			for _, ref := range refs {
				progress, err := client.GetInitiativeProgress(cmd.Context(), ref, window, issueZone())
				if err != nil && named {
					return handleArgumentAPIError(err)
				}
				if err != nil {
					return handleAPIError(err)
				}
				all = append(all, progress)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), all)
			}
			printInitiativesProgress(cmd.OutOrStdout(), all, time.Now())
			return nil
		},
	}
	addWindowFlags(cmd, "issues closed")
	since := cmd.Flags().Lookup("since")
	since.DefValue = recentCloses
	_ = since.Value.Set(recentCloses)
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

	open := unclosedCount(states)
	v.line("  %s  %d/%d done", progressBar(p, states, open), states.Completed, states.Completed+open)
	v.line("  %s", stateCountsLine(p, states))
	if initiative.Claim != nil {
		v.line("  %s", v.fit(claimNote(*initiative.Claim, v.now), 2))
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

// unclosedCount is how many of the issues the states count are neither
// completed nor canceled.
func unclosedCount(s api.IssueStateCounts) int {
	return s.Running + s.Waiting + s.Ready + s.Blocked + s.Triage + s.Deferred + s.Shelved + s.OpenChildren
}

// progressBar fills its done cells by the rule the list's bar keeps, in green,
// then splits the empty cells among the open work: yellow for the running and
// waiting, red for the blocked and plain for the rest. Without color it reads
// as done over done and open.
func progressBar(p gotermstyle.Palette, s api.IssueStateCounts, open int) string {
	filled := progressFilled(s.Completed, open, progressBarCells)
	empty := progressBarCells - filled
	share := func(n int) int {
		if open == 0 {
			return 0
		}
		return (n*empty + open/2) / open
	}
	underWay := share(s.Running + s.Waiting)
	blocked := share(s.Running+s.Waiting+s.Blocked) - underWay
	rest := empty - underWay - blocked
	return p.Green(strings.Repeat("▰", filled)) + p.Yellow(strings.Repeat("▱", underWay)) +
		p.Red(strings.Repeat("▱", blocked)) + strings.Repeat("▱", rest)
}

// stateCountsLine names the done count always, every other state holding an
// issue, and the total, done in green, the work under way in yellow and the
// blocked work in red.
func stateCountsLine(p gotermstyle.Palette, s api.IssueStateCounts) string {
	plain := func(text string) string { return text }
	parts := []string{p.Green(strconv.Itoa(s.Completed) + " done")}
	for _, state := range []struct {
		n     int
		name  string
		color func(string) string
	}{
		{s.Running, "running", p.Yellow},
		{s.Waiting, "waiting", p.Yellow},
		{s.Blocked, "blocked", p.Red},
		{s.Ready, "ready", plain},
		{s.Triage, "in triage", plain},
		{s.Deferred, "deferred", plain},
		{s.Shelved, "shelved", plain},
		{s.OpenChildren, "with open children", plain},
		{s.Canceled, "canceled", plain},
	} {
		if state.n > 0 {
			parts = append(parts, state.color(strconv.Itoa(state.n)+" "+state.name))
		}
	}
	total := s.Completed + s.Canceled + unclosedCount(s)
	return strings.Join(append(parts, strconv.Itoa(total)+" "+plural(total, "issue", "issues")), " · ")
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
// expiry and what it waits on.
func (v progressView) claimedSection(heading string, issues []api.Issue) {
	rows := make([]progressRow, 0, len(issues))
	for _, issue := range issues {
		row := progressRow{number: issue.Number, title: issue.Title}
		if issue.Claim != nil {
			row.note = claimNote(*issue.Claim, v.now)
		}
		rows = append(rows, row)
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
// waits on.
func (v progressView) blockedSection(issues []api.Issue) {
	rows := make([]progressRow, 0, len(issues))
	for _, issue := range issues {
		var waitsOn []string
		for _, dependency := range issue.DependsOn {
			if dependency.Status != api.IssueStatusCompleted && dependency.Status != api.IssueStatusCanceled {
				waitsOn = append(waitsOn, "#"+strconv.Itoa(dependency.Number))
			}
		}
		rows = append(rows, progressRow{number: issue.Number, title: issue.Title, note: "waits on " + strings.Join(waitsOn, ", ")})
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
