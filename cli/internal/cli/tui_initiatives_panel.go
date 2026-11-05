package cli

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/datapointchris/gotermboard"

	"github.com/datapointchris/slate/cli/internal/api"
)

// A description in the panel stops here, so what follows it stays on screen. d and enter
// open the whole of it in place.
const (
	panelDescriptionRows = 18
	panelNextRows        = 4
)

func heading(text string) gotermboard.Line {
	return gotermboard.Line{gotermboard.C(text, gotermboard.Bold)}
}

func plainLine(text string) gotermboard.Line {
	return gotermboard.Line{gotermboard.C(text, "")}
}

func dimLine(text string) gotermboard.Line {
	return gotermboard.Line{gotermboard.C(text, dimCode)}
}

// stateCountsCells is stateCountsLine's counts in their colors, without the canceled,
// which the bar leaves out, and without the total.
func stateCountsCells(s api.IssueStateCounts) gotermboard.Line {
	var line gotermboard.Line
	for _, count := range stateCounts(s) {
		if count.name == "canceled" {
			continue
		}
		if len(line) > 0 {
			line = append(line, gotermboard.C(" · ", ""))
		}
		line = append(line, gotermboard.C(strconv.Itoa(count.n)+" "+count.name, count.code))
	}
	return line
}

func closesLines(closes api.BoardCloses, now time.Time) []gotermboard.Line {
	last := "none yet"
	if closes.LastClosedTS != nil {
		last = "last " + ageOf(closes.LastClosedTS, now) + " ago"
	}
	return []gotermboard.Line{
		{gotermboard.C("Closed, last 14 days  ", gotermboard.Bold), gotermboard.C(sparkLine(closes.ByDay), gotermboard.Green)},
		plainLine(fmt.Sprintf("%d in 24h · %d in 7d · %d in 30d · %s", closes.Last24h, closes.Last7d, closes.Last30d, last)),
	}
}

func tierPanel(label string, tier api.BoardTier, now time.Time) []gotermboard.Line {
	lines := []gotermboard.Line{
		{gotermboard.C(label, tierCode(tier.Priority)+gotermboard.Bold)},
		{},
		stateCountsCells(tier.States),
		plainLine(strconv.Itoa(tier.States.Canceled) + " canceled, not on the bar"),
		{},
	}
	return append(lines, closesLines(tier.Closes, now)...)
}

func (b *initiativesBoard) initiativePanel(initiative api.BoardInitiative, long api.BoardInitiativeText, rows cursorRowReads, read api.InitiativeBoard, now time.Time, width int) []gotermboard.Line {
	lines := []gotermboard.Line{
		{gotermboard.C(strconv.Itoa(initiative.Number)+"  ", gotermboard.Bold), gotermboard.C(priorityName(initiative.Priority), tierCode(initiative.Priority)+gotermboard.Bold)},
		heading(initiative.Name),
	}
	if long.Summary != "" {
		lines = append(lines, plainLine(long.Summary))
	}
	lines = append(lines, gotermboard.Line{}, stateCountsCells(initiative.States))
	if initiative.States.Canceled > 0 {
		lines = append(lines, plainLine(strconv.Itoa(initiative.States.Canceled)+" canceled, not on the bar"))
	}
	if initiative.Status != "active" {
		status := initiative.Status
		if reason := strValue(long.StatusReason); reason != "" {
			status += ": " + reason
		}
		lines = append(lines, gotermboard.Line{gotermboard.C(status, gotermboard.Magenta)})
	}
	if initiative.Claim != nil {
		lines = append(lines, plainLine(claimNote(*initiative.Claim, now)))
	}
	if initiative.PinnedBy != nil {
		lines = append(lines, gotermboard.Line{gotermboard.C("pinned by "+*initiative.PinnedBy, gotermboard.MarkCode)})
	}
	if len(initiative.Repos) > 0 {
		lines = append(lines, dimLine(strings.Join(initiative.Repos, ", ")))
	}
	lines = append(lines, gotermboard.Line{})
	lines = append(lines, closesLines(initiative.Closes, now)...)
	if long.Description != nil {
		lines = append(lines, gotermboard.Line{}, heading("Description"))
		lines = append(lines, b.panelDescription(*long.Description, "d opens the whole of it", width)...)
	}
	lines = append(lines, gotermboard.Line{}, heading("Next"))
	progress, held := rows.progress[initiative.Number]
	if !held {
		return append(lines, dimLine("reading…"), gotermboard.Line{}, heading("Tokens, its whole life"), dimLine("reading…"))
	}
	for at, row := range progress.Next {
		if at == panelNextRows {
			break
		}
		lines = append(lines, gotermboard.Line{gotermboard.C(strconv.Itoa(row.Number), gotermboard.Cyan), gotermboard.C("  "+row.Title, "")})
	}
	if len(progress.Next) == 0 {
		lines = append(lines, dimLine("Nothing is next."))
	}
	lines = append(lines, gotermboard.Line{}, heading("Tokens, its whole life"))
	return append(lines, tokenLines(progress.Totals)...)
}

// tokenLines names each kind's count, and never a total across them.
func tokenLines(totals api.WorkTotals) []gotermboard.Line {
	var lines []gotermboard.Line
	if tokens := totals.Tokens; tokens == nil {
		lines = append(lines, dimLine("None reported."))
	} else {
		lines = append(lines,
			plainLine("uncached input "+groupedDigits(tokens.UncachedInput)),
			plainLine("cache write "+groupedDigits(tokens.CacheWrite)),
			plainLine("cache read "+groupedDigits(tokens.CacheRead)),
			plainLine("output "+groupedDigits(tokens.Output)),
		)
	}
	return append(lines, plainLine(fmt.Sprintf("%d %s · %d %s",
		totals.WorklogCount, plural(totals.WorklogCount, "worklog", "worklogs"),
		totals.SessionCount, plural(totals.SessionCount, "session", "sessions"))))
}

func (b *initiativesBoard) issuePanel(issue api.BoardIssue, excerpt, description string, read bool, now time.Time, width int) []gotermboard.Line {
	facts := []string{issue.Type, priorityName(issue.EffectivePriority)}
	if issue.Repo != nil {
		facts = append(facts, *issue.Repo)
	}
	lines := []gotermboard.Line{
		{gotermboard.C(strconv.Itoa(issue.Number)+"  ", gotermboard.Bold), gotermboard.C(boardStateWord(issue.State), issueStateCode(issue.State))},
		plainLine(issue.Title),
		{},
		plainLine(strings.Join(facts, " · ")),
	}
	if issue.Holder != nil {
		lines = append(lines, plainLine("held by "+*issue.Holder))
	}
	if issue.ClosedTS != nil {
		lines = append(lines, plainLine("closed "+ageOf(issue.ClosedTS, now)+" ago"))
	}
	lines = append(lines, gotermboard.Line{}, heading("Description"))
	if !read {
		if excerpt != "" {
			lines = append(lines, plainLine(excerpt))
		}
		return append(lines, dimLine("reading…"))
	}
	return append(lines, b.panelDescription(description, "enter opens the whole of it", width)...)
}

// panelDescription is a description's first rows.
func (b *initiativesBoard) panelDescription(description, more string, width int) []gotermboard.Line {
	if strings.TrimSpace(description) == "" {
		return []gotermboard.Line{dimLine("No description.")}
	}
	lines := b.descriptionLines(description, width)
	if len(lines) <= panelDescriptionRows {
		return lines
	}
	return slices.Concat(lines[:panelDescriptionRows], []gotermboard.Line{dimLine(more)})
}
