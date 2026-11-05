package cli

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/datapointchris/gotermboard"

	"github.com/datapointchris/slate/cli/internal/api"
)

// dimCode draws what only marks an absence, such as a zero's dot.
const dimCode = "\033[2m"

// sparkGlyphs are a spark line's heights, lowest first.
const sparkGlyphs = "▁▂▃▄▅▆▇█"

var boardColumns = []gotermboard.Column{
	{Title: ""},
	{Title: "DONE", Right: true},
	{Title: "24H", Right: true},
	{Title: "7D", Right: true},
	{Title: "30D", Right: true},
	{Title: "LAST", Right: true},
	{Title: "RUN", Right: true},
	{Title: "BLK", Right: true},
	{Title: "RDY", Right: true},
	{Title: "ISSUES"},
}

// boardText is the long text by number, for one frame.
type boardText struct {
	initiatives map[int]api.BoardInitiativeText
	issues      map[int]string
}

func indexBoardText(read api.InitiativeBoardText) boardText {
	text := boardText{initiatives: make(map[int]api.BoardInitiativeText, len(read.Initiatives)), issues: map[int]string{}}
	for _, initiative := range read.Initiatives {
		text.initiatives[initiative.Number] = initiative
		for _, issue := range initiative.Issues {
			text.issues[issue.Number] = strValue(issue.Excerpt)
		}
	}
	return text
}

// Frame is gotermboard.Board's.
func (b *initiativesBoard) Frame(now time.Time, _ string) gotermboard.Frame {
	read := b.initiatives.Value()
	frame := gotermboard.Frame{Columns: boardColumns}
	if read.ReadTS.IsZero() {
		frame.Strip = []gotermboard.Line{b.stripLine(now, 0, 0)}
		return frame
	}
	ordered, shown := b.orderedAndShown(read)
	showing := 0
	for _, isShown := range shown {
		if isShown {
			showing++
		}
	}
	frame.Strip = []gotermboard.Line{b.stripLine(now, showing, read.Totals.InitiativeCount)}
	frame.StartAt = b.openingKey(read)
	frame.Groups = append(frame.Groups, totalGroup(read, now))
	text := indexBoardText(b.text.Value())
	rows := b.cursorRow.Value()
	for _, tier := range read.Tiers {
		if tier.InitiativeCount == 0 {
			continue
		}
		frame.Groups = append(frame.Groups, b.tierGroup(tier, ordered, shown, text, rows, read, now))
	}
	return frame
}

func (b *initiativesBoard) stripLine(now time.Time, showing, total int) gotermboard.Line {
	line := gotermboard.Line{
		gotermboard.C("slate · initiatives", gotermboard.Bold),
		gotermboard.C("   "+now.Format("15:04:05")+"   ", ""),
		gotermboard.C(b.scope.label(), gotermboard.Cyan),
		gotermboard.C(fmt.Sprintf(" · %d of %d shown   sorted by %s   ", showing, total, boardSorts[b.sort].label), ""),
	}
	if b.status != "" {
		line = append(line, gotermboard.C(b.status+"   ", gotermboard.Magenta))
	}
	return append(line, gotermboard.ReadStatus([]gotermboard.Status{b.initiatives.Status(), b.text.Status(), b.cursorRow.Status()}, now)...)
}

func totalGroup(read api.InitiativeBoard, now time.Time) gotermboard.Group {
	counts := read.Totals.BoardCounts
	done, all := counts.States.Completed, counts.States.Completed+counts.OpenCount
	percent := 0
	if all > 0 {
		percent = (100*done + all/2) / all
	}
	wrapped := append(scaledBar(counts), gotermboard.C(fmt.Sprintf(" %d%%  ", percent), ""), gotermboard.C(sparkLine(counts.Closes.ByDay), gotermboard.Green))
	return gotermboard.Group{
		Key:      "total",
		Title:    gotermboard.Line{gotermboard.C("TOTAL · "+strconv.Itoa(read.Totals.InitiativeCount), gotermboard.Bold)},
		Cells:    boardCells(counts, now),
		WrapLast: wrapped,
		Still:    true,
		Heading:  true,
		Gap:      true,
	}
}

func (b *initiativesBoard) tierGroup(tier api.BoardTier, ordered []api.BoardInitiative, shown map[int]bool, text boardText, rows cursorRowReads, read api.InitiativeBoard, now time.Time) gotermboard.Group {
	var children []gotermboard.Group
	showing := 0
	for _, initiative := range ordered {
		if initiative.Priority != tier.Priority {
			continue
		}
		if shown[initiative.Number] {
			showing++
		}
		children = append(children, b.initiativeGroup(initiative, !shown[initiative.Number], text, rows, read, now))
	}
	label := priorityName(tier.Priority) + " · " + strconv.Itoa(tier.InitiativeCount)
	if showing != tier.InitiativeCount {
		label = fmt.Sprintf("%s · %d of %d", priorityName(tier.Priority), showing, tier.InitiativeCount)
	}
	counts := tier.BoardCounts
	return gotermboard.Group{
		Key:        "tier:" + strconv.Itoa(tier.Priority),
		Title:      gotermboard.Line{gotermboard.C(label, tierCode(tier.Priority)+gotermboard.Bold)},
		Cells:      boardCells(counts, now),
		WrapLast:   append(scaledBar(counts), gotermboard.C("  ", ""), gotermboard.C(sparkLine(counts.Closes.ByDay), gotermboard.Green)),
		Heading:    true,
		FilterOnly: showing == 0,
		Gap:        true,
		Groups:     children,
		Panel:      tierPanel(label, tier, now),
	}
}

func (b *initiativesBoard) initiativeGroup(initiative api.BoardInitiative, beyondScope bool, text boardText, rows cursorRowReads, read api.InitiativeBoard, now time.Time) gotermboard.Group {
	long := text.initiatives[initiative.Number]
	description := strValue(long.Description)
	below := []gotermboard.Line{{gotermboard.C(initiative.Name, "")}}
	if len(initiative.Repos) > 0 {
		below = append(below, gotermboard.Items(gotermboard.Blue, initiative.Repos...))
	}
	return gotermboard.Group{
		Key:        initiativeKey(initiative.Number),
		Title:      gotermboard.Line{gotermboard.C(strconv.Itoa(initiative.Number), gotermboard.Cyan)},
		Cells:      boardCells(initiative.BoardCounts, now),
		WrapLast:   issueBar(initiative.BoardCounts),
		Below:      below,
		Under:      gotermboard.C(strValue(long.Summary), ""),
		Paragraphs: b.describedParagraphs(initiative.Number, description, len(text.initiatives) > 0),
		Folded:     true,
		FilterOnly: beyondScope,
		Groups:     issueGroups(initiative, text, rows, now),
		Text:       description + " " + strValue(long.StatusReason),
		Panel:      initiativePanel(initiative, long, rows, read, now),
	}
}

// describedParagraphs is the description while d holds it open.
func (b *initiativesBoard) describedParagraphs(number int, description string, read bool) []gotermboard.Cell {
	if !b.described[number] {
		return nil
	}
	if !read {
		return []gotermboard.Cell{gotermboard.C("reading…", dimCode)}
	}
	var cells []gotermboard.Cell
	for _, paragraph := range descriptionParagraphs(description) {
		cells = append(cells, gotermboard.C(paragraph, ""))
	}
	if len(cells) == 0 {
		cells = []gotermboard.Cell{gotermboard.C("No description.", dimCode)}
	}
	return append(cells, gotermboard.C("d closes the description", dimCode))
}

// issueGroups is an initiative's issues unfolded: the open first, running at the top and
// then in queue order, with the done and the canceled folded beneath.
func issueGroups(initiative api.BoardInitiative, text boardText, rows cursorRowReads, now time.Time) []gotermboard.Group {
	var running, waiting, queued, done, canceled []gotermboard.Row
	for _, issue := range initiative.Issues {
		row := issueRow(issue, text, rows, now)
		switch issue.State {
		case api.BoardStateRunning:
			running = append(running, row)
		case api.BoardStateWaiting:
			waiting = append(waiting, row)
		case api.IssueStatusCompleted:
			done = append(done, row)
		case api.IssueStatusCanceled:
			canceled = append(canceled, row)
		default:
			queued = append(queued, row)
		}
	}
	number := strconv.Itoa(initiative.Number)
	var groups []gotermboard.Group
	if open := slices.Concat(running, waiting, queued); len(open) > 0 {
		groups = append(groups, gotermboard.Group{
			Key:     "open:" + number,
			Title:   gotermboard.Line{gotermboard.C("open · "+strconv.Itoa(len(open)), gotermboard.Bold)},
			Summary: gotermboard.Line{gotermboard.C("running first, then in queue order", dimCode)},
			Still:   true,
			Heading: true,
			Rows:    open,
		})
	}
	if len(done) > 0 {
		groups = append(groups, gotermboard.Group{
			Key:     "done:" + number,
			Title:   gotermboard.Line{gotermboard.C("done · "+strconv.Itoa(len(done)), gotermboard.Green)},
			Summary: gotermboard.Line{gotermboard.C("newest first", dimCode)},
			Heading: true,
			Folded:  true,
			Rows:    done,
		})
	}
	if len(canceled) > 0 {
		groups = append(groups, gotermboard.Group{
			Key:     "canceled:" + number,
			Title:   gotermboard.Line{gotermboard.C("canceled · "+strconv.Itoa(len(canceled)), "")},
			Heading: true,
			Folded:  true,
			Rows:    canceled,
		})
	}
	return groups
}

func issueRow(issue api.BoardIssue, text boardText, rows cursorRowReads, now time.Time) gotermboard.Row {
	excerpt := text.issues[issue.Number]
	description, read := rows.descriptions[issue.Number]
	var detail []gotermboard.Cell
	if read {
		for _, paragraph := range descriptionParagraphs(description) {
			detail = append(detail, gotermboard.C(paragraph, ""))
		}
		if len(detail) == 0 {
			detail = []gotermboard.Cell{gotermboard.C("No description.", dimCode)}
		}
	} else {
		detail = []gotermboard.Cell{gotermboard.C(cmp.Or(excerpt, "reading…"), dimCode)}
	}
	lead := gotermboard.C(padRight(boardStateWord(issue.State), issueStateWidth)+" ", issueStateCode(issue.State))
	if issue.ClosedTS != nil {
		lead = gotermboard.Ticking(padLeft(ageOf(issue.ClosedTS, now), issueStateWidth)+" ", dimCode)
	}
	return gotermboard.Row{
		Key:    issueKey(issue.Number),
		Span:   gotermboard.Line{lead, gotermboard.C(strconv.Itoa(issue.Number), gotermboard.Cyan), gotermboard.C("  "+issue.Title, "")},
		Under:  gotermboard.C(excerpt, ""),
		Detail: detail,
		Panel:  issuePanel(issue, excerpt, description, read, now),
	}
}

// issueStateWidth is the widest state word an unfolded row draws.
const issueStateWidth = 8

func boardStateWord(state string) string {
	if state == api.BoardStateOpenChildren {
		return "children"
	}
	return state
}

func issueStateCode(state string) string {
	switch state {
	case api.BoardStateRunning, api.BoardStateWaiting:
		return gotermboard.Yellow
	case api.BoardStateBlocked:
		return gotermboard.Red
	case api.IssueStatusCompleted:
		return gotermboard.Green
	}
	return ""
}

// ageOf is an age in the board's one unit, or the dot where nothing happened.
func ageOf(at *time.Time, now time.Time) string {
	if at == nil {
		return "·"
	}
	return gotermboard.Ago(now.Sub(*at))
}

// boardCells is a row's cells on the columns past its title.
func boardCells(counts api.BoardCounts, now time.Time) []gotermboard.Cell {
	done := counts.States.Completed
	last := gotermboard.C("·", dimCode)
	if counts.Closes.LastClosedTS != nil {
		last = gotermboard.Ticking(ageOf(counts.Closes.LastClosedTS, now), "")
	}
	return []gotermboard.Cell{
		gotermboard.C(fmt.Sprintf("%d/%d", done, done+counts.OpenCount), ""),
		countCell(counts.Closes.Last24h, gotermboard.Green),
		countCell(counts.Closes.Last7d, gotermboard.Green),
		countCell(counts.Closes.Last30d, gotermboard.Green),
		last,
		countCell(counts.States.Running+counts.States.Waiting, gotermboard.Yellow),
		countCell(counts.States.Blocked, gotermboard.Red),
		countCell(counts.States.Ready, ""),
	}
}

func countCell(n int, code string) gotermboard.Cell {
	if n == 0 {
		return gotermboard.C("·", dimCode)
	}
	return gotermboard.C(strconv.Itoa(n), code)
}

// The glyphs a bar draws, as the progress bar in `initiatives list` has them.
const (
	barFilledGlyph = "▰"
	barEmptyGlyph  = "▱"
)

// barLine is cells of each kind: done in green, the work under way in yellow, the blocked
// in red and the rest plain.
func barLine(done, underWay, blocked, rest int) gotermboard.Line {
	return gotermboard.Line{
		gotermboard.C(strings.Repeat(barFilledGlyph, done), gotermboard.Green),
		gotermboard.C(strings.Repeat(barFilledGlyph, underWay), gotermboard.Yellow),
		gotermboard.C(strings.Repeat(barEmptyGlyph, blocked), gotermboard.Red),
		gotermboard.C(strings.Repeat(barEmptyGlyph, rest), ""),
	}
}

// issueBar has one cell per issue, so a count on it can be checked by eye. Canceled
// issues stay off it.
func issueBar(counts api.BoardCounts) gotermboard.Line {
	underWay := counts.States.Running + counts.States.Waiting
	return barLine(counts.States.Completed, underWay, counts.States.Blocked, max(counts.OpenCount-underWay-counts.States.Blocked, 0))
}

// scaledBar is issueBar's kinds shared over progressBarCells, for a row holding too many
// issues to draw one cell each. A kind holding any issue keeps a cell, so a blocked issue
// never rounds away.
func scaledBar(counts api.BoardCounts) gotermboard.Line {
	underWay := counts.States.Running + counts.States.Waiting
	kinds := []int{counts.States.Completed, underWay, counts.States.Blocked, max(counts.OpenCount-underWay-counts.States.Blocked, 0)}
	total := 0
	for _, n := range kinds {
		total += n
	}
	if total == 0 {
		return barLine(0, 0, 0, progressBarCells)
	}
	cells := make([]int, len(kinds))
	left := progressBarCells
	for i, n := range kinds {
		cells[i] = n * progressBarCells / total
		if n > 0 {
			cells[i] = max(cells[i], 1)
		}
		left -= cells[i]
	}
	widest := slices.Index(cells, slices.Max(cells))
	cells[widest] += left
	return barLine(cells[0], cells[1], cells[2], cells[3])
}

// sparkLine is one glyph per day, a day with no closes a space.
func sparkLine(days []int) string {
	if len(days) == 0 {
		return ""
	}
	glyphs := []rune(sparkGlyphs)
	most := max(slices.Max(days), 1)
	var b strings.Builder
	for _, closes := range days {
		if closes == 0 {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(glyphs[min(len(glyphs)-1, (closes*len(glyphs)-1)/most)])
	}
	return b.String()
}

// descriptionParagraphs is a description as lines to wrap, with its blank lines and its
// bold markers out, because each line is drawn on its own in a narrow column.
func descriptionParagraphs(description string) []string {
	var paragraphs []string
	for line := range strings.Lines(description) {
		line = strings.TrimSpace(strings.ReplaceAll(line, "**", ""))
		if line != "" {
			paragraphs = append(paragraphs, line)
		}
	}
	return paragraphs
}
