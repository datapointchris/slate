package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/gotermboard"
	"github.com/datapointchris/gotermboard/markdown"

	"github.com/datapointchris/slate/cli/internal/api"
)

const tabQueue = "queue"

// queueSchemaVersion versions what --json prints, whose shape tui-queue.schema.json describes.
const queueSchemaVersion = 1

// The rows 1 and 2 show of the queue, which 3 shows whole.
const (
	queueFirstFew  = 50
	queueFirstMany = 200
)

// queueTitleShown caps the TITLE column, so a claimed row's holder and wait keep room after it.
const queueTitleShown = 72

// errQueueBoardUnanswered is an API older than the queue board, which answers its route as no
// route at all.
var errQueueBoardUnanswered = errors.New("this API answers no queue board, so it is older than this CLI")

var queueColumns = []gotermboard.Column{
	{Title: "#"},
	{Title: "KIND"},
	{Title: "STEP"},
	{Title: "PRI"},
	{Title: "INITIATIVE"},
	{Title: "REPO", Max: 16},
	{Title: "TITLE", Max: queueTitleShown},
	{Title: ""},
}

// queueScope says how many queued rows the tab draws. Every row is read, so the strip counts them
// all and / searches them all.
type queueScope int

const (
	queueScopeFew queueScope = iota
	queueScopeMany
	queueScopeAll
)

// shown counts the first queued rows drawn under the scope, of rows in all.
func (s queueScope) shown(rows int) int {
	switch s {
	case queueScopeFew:
		return min(rows, queueFirstFew)
	case queueScopeMany:
		return min(rows, queueFirstMany)
	}
	return rows
}

func (s queueScope) label() string {
	switch s {
	case queueScopeFew:
		return "first " + strconv.Itoa(queueFirstFew)
	case queueScopeMany:
		return "first " + strconv.Itoa(queueFirstMany)
	}
	return "all"
}

// queueRecord is what the panel draws of a row's record beyond the board's row: its description,
// its rank and its own pin.
type queueRecord struct {
	Kind        string     `json:"kind"`
	Number      int        `json:"number"`
	Description *string    `json:"description"`
	Rank        *float64   `json:"rank"`
	PinnedTS    *time.Time `json:"pinned_ts"`
}

// queueRecordOf is the part of a read by number that the panel draws.
func queueRecordOf(record api.Record) queueRecord {
	read := queueRecord{Kind: record.Kind, Number: record.Number}
	switch {
	case record.Issue != nil:
		read.Description, read.Rank, read.PinnedTS = record.Issue.Description, &record.Issue.Rank, record.Issue.PinnedTS
	case record.Initiative != nil:
		read.Description, read.Rank, read.PinnedTS = record.Initiative.Description, &record.Initiative.Rank, record.Initiative.PinnedTS
	case record.Idea != nil:
		read.Description, read.Rank, read.PinnedTS = record.Idea.Description, record.Idea.Rank, record.Idea.PinnedTS
	}
	return read
}

// queueRecordFault is the last read by number that failed, on which record, and why.
type queueRecordFault struct {
	number int
	fault  string
}

// queueBoard is the queue tab: every kind of open work in the order `slate next` takes it, with
// the claimed work apart. Its reads run off the program's goroutine and touch only their sources,
// the atomics and their own failure. The keys and Frame run on it.
type queueBoard struct {
	client *api.Client
	// writer runs the writes the keys start, one at a time across this tab and the initiatives tab.
	writer          *rankAndPinWriter
	board           *gotermboard.Source[api.QueueBoard]
	focus           *gotermboard.Source[api.FocusStanding]
	records         *gotermboard.Source[map[int]queueRecord]
	cursor          atomic.Pointer[int]
	lastRecordFault atomic.Pointer[queueRecordFault]
	// scope, markdown and lines belong to the program's goroutine, with the keys and Frame. lines
	// is each line's row as the last Frame drew it, which CursorOn and the keys look the cursor up in.
	scope    queueScope
	markdown *markdown.Renderer
	lines    map[string]queueLine
	// boardFailure, focusFailure and recordFailure are each read's last failure, for --once and
	// --json to exit on.
	boardFailure, focusFailure, recordFailure error
}

// queueLine is a row as the last frame drew it: the row, and its place in the queue, or -1 for a
// claimed row.
type queueLine struct {
	row api.QueueBoardRow
	at  int
}

func newQueueBoard(client *api.Client, writer *rankAndPinWriter) *queueBoard {
	zone := issueZone()
	b := &queueBoard{client: client, writer: writer, markdown: markdown.New(nil)}
	b.board = gotermboard.NewSource("queue", boardReadEvery, func(ctx context.Context) (api.QueueBoard, string, error) {
		read, err := client.GetQueueBoard(ctx, zone)
		if noRouteFor(err) {
			err = errQueueBoardUnanswered
		}
		b.boardFailure = err
		return read, "", err
	})
	b.focus = gotermboard.NewSource("focus", boardReadEvery, func(ctx context.Context) (api.FocusStanding, string, error) {
		standing, err := client.GetFocusStanding(ctx, zone)
		b.focusFailure = err
		return standing, "", err
	})
	b.records = gotermboard.NewSource("cursor's record", cursorRowReadEvery, b.readCursorRecord)
	return b
}

// readCursorRecord reads the cursor's record by its number, keeping every record read before, so
// a row returned to draws at once.
func (b *queueBoard) readCursorRecord(ctx context.Context) (map[int]queueRecord, string, error) {
	held := b.records.Value()
	number := b.cursor.Load()
	if number == nil {
		return held, "", nil
	}
	record, err := b.client.GetRecord(ctx, strconv.Itoa(*number), issueZone())
	b.recordFailure = err
	if err != nil {
		b.lastRecordFault.Store(&queueRecordFault{number: *number, fault: err.Error()})
		return held, "", err
	}
	b.lastRecordFault.Store(nil)
	next := make(map[int]queueRecord, len(held)+1)
	maps.Copy(next, held)
	next[*number] = queueRecordOf(record)
	return next, "", nil
}

// Sources is gotermboard.Board's.
func (b *queueBoard) Sources() []gotermboard.Refresher {
	return []gotermboard.Refresher{b.board, b.focus, b.records}
}

// Groupings is gotermboard.Board's.
func (b *queueBoard) Groupings() []string { return nil }

// CursorOn is gotermboard.CursorBoard's. A record reads on the next tick where none is held, so
// holding a key down reads only the record it stops on.
func (b *queueBoard) CursorOn(key string) {
	line, ok := b.lines[key]
	if !ok {
		b.cursor.Store(nil)
		return
	}
	number := line.row.Number
	b.cursor.Store(&number)
	if _, held := b.records.Value()[number]; !held {
		b.records.Force()
	}
}

// readOnce is every read a printed frame draws from, the first row's record last, because the
// board says which row that is.
func (b *queueBoard) readOnce(ctx context.Context) error {
	gotermboard.RefreshAll(ctx, b.board, b.focus)
	if err := errors.Join(b.boardFailure, b.focusFailure); err != nil {
		return err
	}
	if queue := b.board.Value().Queue; len(queue) > 0 {
		b.cursor.Store(&queue[0].Number)
		b.records.Refresh(ctx)
	}
	return b.recordFailure
}

// Receive is gotermboard.ReceivingBoard's. A write on either writing tab changes the queue, so its
// end reads the board and the cursor's record again.
func (b *queueBoard) Receive(m *gotermboard.Model, msg tea.Msg) tea.Cmd {
	if b.writer.ended(m, msg) {
		b.board.Force()
		b.records.Force()
	}
	return nil
}

// UnderWay is gotermboard.BusyBoard's.
func (b *queueBoard) UnderWay() string { return b.writer.writing }

// Actions is gotermboard.Keyed's.
func (b *queueBoard) Actions() []gotermboard.Action {
	scoped := func(scope queueScope) func(*gotermboard.Model) tea.Cmd {
		return func(m *gotermboard.Model) tea.Cmd {
			b.scope = scope
			m.Rebuild()
			return nil
		}
	}
	return []gotermboard.Action{
		{Keys: []string{"1"}, Label: fmt.Sprintf("show the first %d rows of the queue", queueFirstFew), Footer: "1/2/3 scope", Run: scoped(queueScopeFew)},
		{Keys: []string{"2"}, Label: fmt.Sprintf("show the first %d rows of the queue", queueFirstMany), Run: scoped(queueScopeMany)},
		{Keys: []string{"3"}, Label: "show every row of the queue", Run: scoped(queueScopeAll)},
		{Keys: []string{"enter"}, Label: "open the row's placement and description under it", Footer: "enter open", Run: func(m *gotermboard.Model) tea.Cmd { return m.Toggle() }},
		{Keys: []string{"P"}, Label: "pin the row at the top of slate next, or unpin it", Footer: "P pin", Run: b.pin, Applies: b.onRow},
		{Keys: []string{"<"}, Label: "move the row ahead of the one above it in the queue", Footer: "</> reorder", Run: b.reorder(true), Applies: b.onQueuedRow},
		{Keys: []string{">"}, Label: "move the row behind the one below it in the queue", Run: b.reorder(false), Applies: b.onQueuedRow},
	}
}

func (b *queueBoard) onRow(m *gotermboard.Model) bool {
	_, ok := b.lines[m.Cursor()]
	return ok
}

func (b *queueBoard) onQueuedRow(m *gotermboard.Model) bool {
	line, ok := b.lines[m.Cursor()]
	return ok && line.at >= 0
}

// pin is P: it pins the row's record, or unpins it where its own pin holds it, through the writer
// both writing tabs hold. The record's own pin comes with its read by number, because a row's
// pinned says only that some pin holds its place.
func (b *queueBoard) pin(m *gotermboard.Model) tea.Cmd {
	line, ok := b.lines[m.Cursor()]
	if !ok {
		return nil
	}
	row := line.row
	record, read := b.records.Value()[row.Number]
	if !read {
		m.Say(fmt.Sprintf("%d's pin is still being read, so press P again once its panel shows it", row.Number))
		return nil
	}
	return b.writer.pin(m, placedRecord{kind: row.Kind, number: row.Number, title: row.Title}, record.PinnedTS != nil, nil)
}

// reorder is < and >: it moves the row past its neighbor in the queue by the API's move rules,
// says why where they move it nowhere, and asks first where the move passes rows hidden by the scope.
func (b *queueBoard) reorder(ahead bool) func(*gotermboard.Model) tea.Cmd {
	return func(m *gotermboard.Model) tea.Cmd {
		line, ok := b.lines[m.Cursor()]
		if !ok || line.at < 0 {
			return nil
		}
		read := b.board.Value()
		target := queueMoveFor(read, b.scope.shown(len(read.Queue)), line.at, ahead)
		if target.refused != "" {
			m.Say(target.refused)
			return nil
		}
		row := line.row
		return b.writer.reorder(m, placedRecord{kind: row.Kind, number: row.Number, title: row.Title}, ahead, target.to, target.reasons)
	}
}

// queueRowKey keys a row by its record, which sits once in the queue or once under its holder.
func queueRowKey(number int) string { return "queue-row:" + strconv.Itoa(number) }

// Frame is gotermboard.Board's.
func (b *queueBoard) Frame(now time.Time, _ string) gotermboard.Frame {
	frame := gotermboard.Frame{Columns: queueColumns}
	status := b.board.Status()
	lines := map[string]queueLine{}
	b.lines = lines
	if status.ReadAt.IsZero() {
		frame.Strip = []gotermboard.Line{b.stripLine(now, api.QueueBoard{}, false)}
		if status.Fault != "" {
			frame.Strip = append(frame.Strip, gotermboard.Line{})
			frame.Groups = []gotermboard.Group{queueNotice(gotermboard.C("The queue is unread: "+status.Fault, gotermboard.Red))}
		}
		return frame
	}
	read := b.board.Value()
	frame.Strip = []gotermboard.Line{b.stripLine(now, read, true), {}}
	shown := b.scope.shown(len(read.Queue))
	queued := gotermboard.Group{Key: "queue", Heading: true, Still: true}
	past := gotermboard.Group{
		Key: "queue:past-the-scope", Heading: true, FilterOnly: true,
		Span: gotermboard.Line{gotermboard.C(fmt.Sprintf("past the %s · %d", b.scope.label(), len(read.Queue)-shown), tierTone)},
	}
	for at, row := range read.Queue {
		drawn := b.queueRow(read, row, now)
		lines[drawn.Key] = queueLine{row: row, at: at}
		if at < shown {
			queued.Rows = append(queued.Rows, drawn)
		} else {
			past.Rows = append(past.Rows, drawn)
		}
	}
	if len(read.Queue) == 0 {
		queued.Below = []gotermboard.Line{dimLine("Nothing is queued.")}
	}
	frame.Groups = append(frame.Groups, queued)
	if len(past.Rows) > 0 {
		frame.Groups = append(frame.Groups, past)
	}
	if len(read.Claimed) > 0 {
		claimed := gotermboard.Group{
			Key: "claimed", Heading: true,
			Span: gotermboard.Line{gotermboard.C(fmt.Sprintf("claimed · %d", len(read.Claimed)), tierTone)},
		}
		for _, row := range read.Claimed {
			drawn := b.queueRow(read, row, now)
			lines[drawn.Key] = queueLine{row: row, at: -1}
			claimed.Rows = append(claimed.Rows, drawn)
		}
		frame.Groups = append(frame.Groups, claimed)
	}
	if len(read.Queue) > 0 {
		frame.StartAt = queueRowKey(read.Queue[0].Number)
	}
	return frame
}

// queueNotice says why the table holds no row, where the rows would be.
func queueNotice(said gotermboard.Cell) gotermboard.Group {
	return gotermboard.Group{
		Key: "queue", Title: gotermboard.Line{gotermboard.C("queue", tierTone)},
		Heading: true, Still: true, Below: []gotermboard.Line{{said}},
	}
}

func (b *queueBoard) stripLine(now time.Time, read api.QueueBoard, counted bool) gotermboard.Line {
	line := gotermboard.Line{gotermboard.C("slate · queue", gotermboard.Bold), gotermboard.C("   ", "")}
	if counted {
		line = append(line, gotermboard.C(fmt.Sprintf("%d open · %d claimed · %s", len(read.Queue), len(read.Claimed), b.scope.label()), ""), gotermboard.C("   ", ""))
	}
	// The record read ticks without reading while the cursor holds a record already read, so only
	// its fault reaches the strip, and the age is the board's.
	records := b.records.Status()
	records.ReadAt = time.Time{}
	return append(line, gotermboard.ReadStatus([]gotermboard.Status{b.board.Status(), b.focus.Status(), records}, now)...)
}

// queueRowEntities is what a row names: its record, an issue's initiative, and its pursuits, so t
// lands on each one's tab.
func queueRowEntities(row api.QueueBoardRow) []gotermboard.EntityRef {
	refs := recordAndPursuits(row.Kind, row.Number, row.Pursuits)
	if row.Initiative != nil {
		refs = slices.Insert(refs, 1, recordEntity(entityKindInitiative, row.Initiative.Number))
	}
	return refs
}

// queueRow is a row on the columns, with its pin or its holder after the title. Enter opens its
// panel under it, and the side panel draws it beside the table.
func (b *queueBoard) queueRow(read api.QueueBoard, row api.QueueBoardRow, now time.Time) gotermboard.Row {
	initiative := gotermboard.C("—", dimCode)
	text := []string{filterNumber(row.Number)}
	if row.Initiative != nil {
		initiative = gotermboard.C(strconv.Itoa(row.Initiative.Number), "")
		text = append(text, row.Initiative.Name)
	}
	if row.Holder != nil {
		text = append(text, *row.Holder)
	}
	return gotermboard.Row{
		Key:      queueRowKey(row.Number),
		Entities: queueRowEntities(row),
		Cells: []gotermboard.Cell{
			gotermboard.C(strconv.Itoa(row.Number), gotermboard.Cyan),
			gotermboard.C(row.Kind, ""),
			gotermboard.C(row.Step, ""),
			gotermboard.C(queuePriority(row), tierCode(row.Priority)),
			initiative,
			gotermboard.C(queueRepos(row.Repos), dimCode),
			gotermboard.C(row.Title, ""),
			queueRowNote(row),
		},
		Text:        strings.Join(append(text, row.Repos...), " "),
		ReaderTitle: fmt.Sprintf("%s %d", row.Kind, row.Number),
		Document:    func(width int) []gotermboard.Line { return b.queuePanel(read, row, width, now) },
	}
}

// queuePriority names the row's sorting priority, starred where it is not the record's own.
func queuePriority(row api.QueueBoardRow) string {
	if row.Priority != row.OwnPriority {
		return priorityName(row.Priority) + "*"
	}
	return priorityName(row.Priority)
}

// queueRepos is the first repo, then how many more, so a cut never halves a repo's name.
func queueRepos(repos []string) string {
	switch len(repos) {
	case 0:
		return noneMark
	case 1:
		return repos[0]
	}
	return fmt.Sprintf("%s, +%d", repos[0], len(repos)-1)
}

// queueRowNote is what follows the title: who holds a claimed row and what its claim waits on,
// else whether a pin holds its place.
func queueRowNote(row api.QueueBoardRow) gotermboard.Cell {
	if row.Holder != nil {
		note := "held by " + shortHolder(*row.Holder)
		if row.Waiting != nil {
			note += " · waiting on " + row.Waiting.WaitingOn
		}
		return gotermboard.C(note, gotermboard.Yellow)
	}
	if row.Pinned {
		return gotermboard.C("pinned", gotermboard.MarkCode)
	}
	return gotermboard.C("", "")
}

// shortHolder is a holder as a person tells them apart: an agent's session by its first group, and
// any other holder whole.
func shortHolder(holder string) string {
	if _, session, found := strings.Cut(holder, "/"); found && session != "" {
		return shortSession(session)
	}
	return holder
}

// queuePanel is why the row sits where it does, as Slurm's sprio splits a job's priority: what
// placed it, its own pin and its rank. Its pursuits' factors follow under their own heading,
// because the factor places nothing. Then its claim, each held issue whose paths meet its own,
// and its description, read by number.
func (b *queueBoard) queuePanel(read api.QueueBoard, row api.QueueBoardRow, width int, now time.Time) []gotermboard.Line {
	record, recordRead := b.records.Value()[row.Number]
	lines := []gotermboard.Line{
		heading(fmt.Sprintf("%d · %s · %s · %s", row.Number, row.Kind, row.Step, priorityName(row.Priority))),
		{gotermboard.C("placed by  ", dimCode), gotermboard.C(placedBySentence(read, row), "")},
		{gotermboard.C("pin        ", dimCode), gotermboard.C(pinSentence(read, row, record, recordRead, now), "")},
		{gotermboard.C("rank       ", dimCode), gotermboard.C(rankSentence(record, recordRead), "")},
		{},
		heading("standing"),
	}
	lines = append(lines, b.standingLines(row)...)
	if row.Holder != nil {
		lines = append(lines, gotermboard.Line{}, heading("claim"), plainLine(*row.Holder))
		if row.HeldThrough != nil {
			lines = append(lines, plainLine("through initiative "+recordName(read, *row.HeldThrough)))
		}
		if row.ExpiresTS != nil {
			lines = append(lines, plainLine("holds until "+localClock(*row.ExpiresTS, now)))
		}
		if wait := row.Waiting; wait != nil {
			lines = append(lines, gotermboard.Line{
				gotermboard.C("waits on "+wait.WaitingOn, gotermboard.Yellow),
				gotermboard.C("   "+spanClock(wait.StartedTS, wait.EndedTS, now), dimCode),
			})
		}
	}
	for _, overlap := range row.Overlaps {
		lines = append(lines, gotermboard.Line{}, gotermboard.Line{gotermboard.C(
			fmt.Sprintf("its paths meet %d %s, held by %s, on %s", overlap.Number, overlap.Title, shortHolder(overlap.Holder), strings.Join(overlap.Paths, ", ")),
			gotermboard.Yellow,
		)})
	}
	lines = append(lines, gotermboard.Line{})
	fault := b.lastRecordFault.Load()
	switch {
	case recordRead && strings.TrimSpace(strValue(record.Description)) != "":
		return append(lines, b.markdown.Lines(*record.Description, min(width, readingWidth))...)
	case recordRead:
		return append(lines, dimLine("No description."))
	case fault != nil && fault.number == row.Number:
		return append(lines, gotermboard.Line{gotermboard.C("The record is unread: "+fault.fault, gotermboard.Red)})
	}
	return append(lines, dimLine("reading…"))
}

// recordName is a record's number with its title, where a row on the board names it.
func recordName(read api.QueueBoard, number int) string {
	for _, row := range slices.Concat(read.Queue, read.Claimed) {
		switch {
		case row.Number == number:
			return fmt.Sprintf("%d %s", number, row.Title)
		case row.Initiative != nil && row.Initiative.Number == number:
			return fmt.Sprintf("%d %s", number, row.Initiative.Name)
		}
	}
	return strconv.Itoa(number)
}

// placedBySentence names what set the row's place, one hop back, with the record it names, and
// the record's own priority where that is not what placed it.
func placedBySentence(read api.QueueBoard, row api.QueueBoardRow) string {
	own := "its own priority is " + priorityName(row.OwnPriority)
	switch {
	case row.PlacedBy == "own":
		return "its own priority and rank"
	case row.PlacedBy == "pin":
		return "its own pin"
	case row.PlacedBy == "initiative" && row.Initiative != nil:
		return fmt.Sprintf("initiative %s · %s", recordName(read, row.Initiative.Number), own)
	case row.PlacedBy == "parent" && row.RaisedBy != nil:
		return fmt.Sprintf("its parent %s · %s", recordName(read, *row.RaisedBy), own)
	case row.PlacedBy == "waiter" && row.RaisedBy != nil:
		return fmt.Sprintf("%s, which waits on it · %s", recordName(read, *row.RaisedBy), own)
	}
	return orDash(row.PlacedBy)
}

// pinSentence is the record's own pin, and where none is, the pin that holds its place through
// what placed it.
func pinSentence(read api.QueueBoard, row api.QueueBoardRow, record queueRecord, recordRead bool, now time.Time) string {
	switch {
	case !recordRead:
		return "reading…"
	case record.PinnedTS != nil:
		return "pinned " + localClock(*record.PinnedTS, now)
	case !row.Pinned:
		return "none"
	case row.RaisedBy != nil:
		return "none of its own · " + recordName(read, *row.RaisedBy) + "'s pin holds its place"
	case row.Initiative != nil:
		return "none of its own · initiative " + strconv.Itoa(row.Initiative.Number) + "'s pin holds its place"
	}
	return "none of its own"
}

// rankSentence is the record's rank, which orders it within its priority and its slot.
func rankSentence(record queueRecord, recordRead bool) string {
	switch {
	case !recordRead:
		return "reading…"
	case record.Rank == nil:
		return noneMark
	}
	return strconv.FormatFloat(*record.Rank, 'f', -1, 64)
}

// standingLines is each of the row's pursuits with its factor against the focus in force.
func (b *queueBoard) standingLines(row api.QueueBoardRow) []gotermboard.Line {
	if len(row.Pursuits) == 0 {
		return []gotermboard.Line{dimLine("  in no pursuit")}
	}
	status := b.focus.Status()
	if status.ReadAt.IsZero() {
		if status.Fault != "" {
			return []gotermboard.Line{{gotermboard.C("  The focus is unread: "+status.Fault, gotermboard.Red)}}
		}
		return []gotermboard.Line{dimLine("  reading…")}
	}
	standing := b.focus.Value()
	width := 0
	for _, pursuit := range row.Pursuits {
		width = max(width, len(pursuit.Name))
	}
	lines := make([]gotermboard.Line, 0, len(row.Pursuits))
	for _, pursuit := range row.Pursuits {
		lines = append(lines, gotermboard.Line{
			gotermboard.C("  "+padRight(pursuit.Name, width), ""),
			gotermboard.C("   "+factorSaid(standing, pursuit.Number), dimCode),
		})
	}
	return lines
}

// factorSaid is a pursuit's factor against the focus in force, as the pursuits tab draws it.
func factorSaid(standing api.FocusStanding, number int) string {
	at := slices.IndexFunc(standing.Pursuits, func(pursuit api.PursuitStanding) bool { return pursuit.Number == number })
	switch {
	case at < 0:
		return "not in the standing"
	case standing.Pursuits[at].Factor == nil:
		return "factor ·"
	}
	return "factor " + strconv.FormatFloat(*standing.Pursuits[at].Factor, 'f', 2, 64)
}

// queueSnapshot is what --json prints, as tui-queue.schema.json describes it.
type queueSnapshot struct {
	SchemaVersion  int                  `json:"schema_version"`
	TakenAt        time.Time            `json:"taken_at"`
	Tab            string               `json:"tab"`
	Board          api.QueueBoard       `json:"board"`
	PursuitFactors []pursuitFactor      `json:"pursuit_factors"`
	CursorRecord   *queueRecord         `json:"cursor_record"`
	Reads          []gotermboard.Status `json:"reads"`
}

// pursuitFactor is one pursuit's factor against the focus in force, under the names `slate focus
// show --json` gives them.
type pursuitFactor struct {
	Number int      `json:"number"`
	Name   string   `json:"name"`
	Factor *float64 `json:"factor"`
}

// Snapshot is what --json prints of the tab.
func (b *queueBoard) Snapshot(now time.Time) queueSnapshot {
	read := b.board.Value()
	read.Queue, read.Claimed = printedQueueRows(read.Queue), printedQueueRows(read.Claimed)
	factors := []pursuitFactor{}
	for _, pursuit := range b.focus.Value().Pursuits {
		factors = append(factors, pursuitFactor{Number: pursuit.Number, Name: pursuit.Name, Factor: pursuit.Factor})
	}
	snapshot := queueSnapshot{
		SchemaVersion: queueSchemaVersion, TakenAt: now, Tab: tabQueue, Board: read, PursuitFactors: factors,
		Reads: []gotermboard.Status{b.board.Status(), b.focus.Status(), b.records.Status()},
	}
	if number := b.cursor.Load(); number != nil {
		if record, held := b.records.Value()[*number]; held {
			snapshot.CursorRecord = &record
		}
	}
	return snapshot
}

// printedQueueRows is each row with every list it holds printed as a list, as the API answers it.
func printedQueueRows(rows []api.QueueBoardRow) []api.QueueBoardRow {
	printed := make([]api.QueueBoardRow, 0, len(rows))
	for _, row := range rows {
		row.Repos, row.Pursuits, row.Overlaps = listed(row.Repos), listed(row.Pursuits), listed(row.Overlaps)
		printed = append(printed, row)
	}
	return printed
}
