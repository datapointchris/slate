package cli

import (
	"cmp"
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

const tabDocuments = "documents"

// documentsSchemaVersion is the shape tui-documents.schema.json describes, which --json prints.
const documentsSchemaVersion = 1

// The list's bound is in records, so a record's documents are never split. + and - move it by a
// step, between one step and the most one read answers.
const (
	documentRecordsStep = 50
	documentRecordsMost = api.RecordLimitMost
)

// errDocumentsLackRecord is an API older than the record on each listed document. It answers
// the list with no X-Total-Records, so the list cannot group by record.
var errDocumentsLackRecord = errors.New("this API answers no record on documents, so it is older than this CLI")

// documentKeyPrefix keys a document's row by its address, as 4271/plan.
const documentKeyPrefix = "document:"

func documentKey(address string) string { return documentKeyPrefix + address }

func documentRecordKey(number int) string { return "record:" + strconv.Itoa(number) }

var documentColumns = []gotermboard.Column{
	{Title: "DOCUMENT"},
	{Title: "KIND"},
	{Title: "VER", Right: true},
	{Title: "WRITTEN"},
	{Title: "WORKLOG"},
	{Title: "COMMENTS", Right: true},
}

// documentsRead is one read of the list, with the bound in records it asked for.
type documentsRead struct {
	list    api.DocumentList
	records int
}

// documentBodies is each document read whole, by address, at the version read. A body stays
// until the list shows a newer version or another comment count, so the poll reads no body.
type documentBodies map[string]api.Document

// documentFault is the last body read that failed, and why.
type documentFault struct {
	address string
	fault   string
}

// documentsBoard is the documents tab. Its reads run off the program's goroutine and touch only
// their sources, the atomics and readFailure. The keys and Frame run on it.
type documentsBoard struct {
	client *api.Client
	// records is the bound the next list read asks for, which + and - move.
	records atomic.Int64
	list    *gotermboard.Source[documentsRead]
	bodies  *gotermboard.Source[documentBodies]
	// listed is the newest list, stored before its source holds it, so a body read that a newer
	// version forced checks against that version.
	listed    atomic.Pointer[api.DocumentList]
	cursor    atomic.Pointer[string]
	bodyFault atomic.Pointer[documentFault]
	markdown  *markdown.Renderer
	// readFailure is the list read's last failure, for --once and --json to exit on.
	readFailure error
}

func newDocumentsBoard(client *api.Client) *documentsBoard {
	b := &documentsBoard{client: client, markdown: markdown.New(nil)}
	b.records.Store(documentRecordsStep)
	b.list = gotermboard.NewSource("documents", boardTextReadEvery, b.readList)
	b.bodies = gotermboard.NewSource("cursor's document", boardTextReadEvery, b.readCursorDocument)
	return b
}

// readList reads the documents on the newest records the bound holds. Where the cursor's
// document shows a newer version than its kept body, the body reads on the next tick.
func (b *documentsBoard) readList(ctx context.Context) (documentsRead, string, error) {
	records := int(b.records.Load())
	list, err := b.client.ListDocuments(ctx, api.DocumentFilter{RecordLimit: &records}, nil)
	if err == nil && list.TotalRecords == nil {
		err = errDocumentsLackRecord
	}
	b.readFailure = err
	if err != nil {
		return documentsRead{}, "", err
	}
	b.listed.Store(&list)
	if _, due := b.cursorDocumentDue(b.bodies.Value()); due {
		b.bodies.Force()
	}
	return documentsRead{list: list, records: records}, "", nil
}

// readCursorDocument reads the cursor's document at its latest version, where the kept body is
// older or missing, and otherwise reads nothing.
func (b *documentsBoard) readCursorDocument(ctx context.Context) (documentBodies, string, error) {
	held := b.bodies.Value()
	listed, due := b.cursorDocumentDue(held)
	if !due {
		return held, "", nil
	}
	document, err := b.client.GetDocument(ctx, strconv.Itoa(listed.RecordNumber), listed.Name, nil)
	if err != nil {
		b.bodyFault.Store(&documentFault{address: listed.Address, fault: err.Error()})
		return held, "", err
	}
	b.bodyFault.Store(nil)
	next := make(documentBodies, len(held)+1)
	maps.Copy(next, held)
	next[listed.Address] = document
	return next, "", nil
}

// cursorDocumentDue is the cursor's document as the newest list has it, and whether held lacks
// its body at that version and comment count. A line comment adds no version, so the count is
// what shows one added since the body was read.
func (b *documentsBoard) cursorDocumentDue(held documentBodies) (api.ListedDocument, bool) {
	key, list := b.cursor.Load(), b.listed.Load()
	if key == nil || list == nil {
		return api.ListedDocument{}, false
	}
	address, ok := strings.CutPrefix(*key, documentKeyPrefix)
	if !ok {
		return api.ListedDocument{}, false
	}
	at := slices.IndexFunc(list.Documents, func(document api.ListedDocument) bool { return document.Address == address })
	if at < 0 {
		return api.ListedDocument{}, false
	}
	listed := list.Documents[at]
	body, read := held[address]
	return listed, !read || body.Version.Version < listed.Latest.Version || len(body.Comments) != listed.CommentCount
}

// Sources is gotermboard.Board's.
func (b *documentsBoard) Sources() []gotermboard.Refresher {
	return []gotermboard.Refresher{b.list, b.bodies}
}

// Groupings is gotermboard.Board's.
func (b *documentsBoard) Groupings() []string { return nil }

// Look is gotermboard.LookBoard's. Each group's header holds its record's title, so the column
// titles take a line of their own. Every row opens, so a chevron would tell none apart.
func (b *documentsBoard) Look(int) gotermboard.Look {
	return gotermboard.Look{Rules: true, NoRowChevrons: true}
}

// CursorOn is gotermboard.CursorBoard's. The body reads on the next tick, so holding a key down
// reads only the document it stops on.
func (b *documentsBoard) CursorOn(key string) {
	b.cursor.Store(&key)
	if _, due := b.cursorDocumentDue(b.bodies.Value()); due {
		b.bodies.Force()
	}
}

// readOnce is every read a printed frame draws from, the first document's body last, because
// the list says which that is.
func (b *documentsBoard) readOnce(ctx context.Context) error {
	gotermboard.RefreshAll(ctx, b.list)
	if b.readFailure != nil {
		return b.readFailure
	}
	if records := byRecord(b.list.Value().list.Documents); len(records) > 0 {
		opening := documentKey(records[0].documents[0].Address)
		b.cursor.Store(&opening)
		b.bodies.Refresh(ctx)
	}
	return nil
}

// recordDocuments is one record's documents, as a group of the tab.
type recordDocuments struct {
	record    api.RecordSummary
	documents []api.ListedDocument
}

// byRecord groups the documents under their records. The record written to last comes first,
// and so does each record's document written last, so the one just written heads the tab.
func byRecord(documents []api.ListedDocument) []recordDocuments {
	newest := slices.Clone(documents)
	slices.SortStableFunc(newest, func(a, b api.ListedDocument) int { return b.Latest.WrittenTS.Compare(a.Latest.WrittenTS) })
	var records []recordDocuments
	at := map[int]int{}
	for _, document := range newest {
		group, seen := at[document.Record.Number]
		if !seen {
			group = len(records)
			at[document.Record.Number] = group
			records = append(records, recordDocuments{record: document.Record})
		}
		records[group].documents = append(records[group].documents, document)
	}
	return records
}

// boundMoved is the bound moved by steps, held between one step and the most one read answers.
func boundMoved(records, steps int) int {
	return min(max(records+steps*documentRecordsStep, documentRecordsStep), documentRecordsMost)
}

// Actions is gotermboard.Keyed's.
func (b *documentsBoard) Actions() []gotermboard.Action {
	bound := func(steps int) func(*gotermboard.Model) tea.Cmd {
		return func(m *gotermboard.Model) tea.Cmd {
			records := boundMoved(int(b.records.Load()), steps)
			b.records.Store(int64(records))
			b.list.Force()
			m.Say(fmt.Sprintf("reading the documents on the %d records written to last", records))
			return nil
		}
	}
	return []gotermboard.Action{
		{
			Keys: []string{"enter"}, Label: "fold a record's documents, or read a document's latest version full screen", Footer: "enter read",
			Run: func(m *gotermboard.Model) tea.Cmd {
				if listed, ok := b.documentAt(m.Cursor()); ok {
					m.Read(documentTitle(listed), b.documentRead(listed))
					return nil
				}
				return m.Toggle()
			},
		},
		{
			Keys: []string{"+", "="}, Label: fmt.Sprintf("read the documents on %d records more", documentRecordsStep), Footer: "+/- records", Run: bound(1),
			Applies: func(*gotermboard.Model) bool { return b.moreToRead() },
		},
		{
			Keys: []string{"-"}, Label: fmt.Sprintf("read the documents on %d records fewer", documentRecordsStep), Run: bound(-1),
			Applies: func(*gotermboard.Model) bool { return b.records.Load() > documentRecordsStep },
		},
	}
}

// moreToRead says a record holding a document lies past the bound, and the bound can grow.
func (b *documentsBoard) moreToRead() bool {
	records := int(b.records.Load())
	total := b.list.Value().list.TotalRecords
	return total != nil && records < min(*total, documentRecordsMost)
}

// documentAt is the document a row's key names, from the last list read.
func (b *documentsBoard) documentAt(key string) (api.ListedDocument, bool) {
	address, ok := strings.CutPrefix(key, documentKeyPrefix)
	if !ok {
		return api.ListedDocument{}, false
	}
	documents := b.list.Value().list.Documents
	at := slices.IndexFunc(documents, func(document api.ListedDocument) bool { return document.Address == address })
	if at < 0 {
		return api.ListedDocument{}, false
	}
	return documents[at], true
}

// Frame is gotermboard.Board's.
func (b *documentsBoard) Frame(now time.Time, _ string) gotermboard.Frame {
	frame := gotermboard.Frame{Columns: documentColumns}
	status := b.list.Status()
	if status.ReadAt.IsZero() {
		frame.Strip = []gotermboard.Line{b.stripLine(now, documentsRead{}, 0)}
		if status.Fault != "" {
			frame.Strip = append(frame.Strip, gotermboard.Line{})
			frame.Groups = []gotermboard.Group{documentsNotice(gotermboard.C("The documents are unread: "+status.Fault, gotermboard.Red))}
		}
		return frame
	}
	read := b.list.Value()
	records := byRecord(read.list.Documents)
	frame.Strip = []gotermboard.Line{b.stripLine(now, read, len(records)), {}}
	if len(records) == 0 {
		frame.Groups = []gotermboard.Group{documentsNotice(gotermboard.C("No record holds a document.", dimCode))}
		return frame
	}
	for at, record := range records {
		frame.Groups = append(frame.Groups, b.recordGroup(record, at > 0, now))
	}
	frame.StartAt = documentKey(records[0].documents[0].Address)
	return frame
}

// documentsNotice says why the table holds no record, where the rows would be.
func documentsNotice(said gotermboard.Cell) gotermboard.Group {
	return gotermboard.Group{
		Key: "documents", Title: gotermboard.Line{gotermboard.C("documents", tierTone)},
		Heading: true, Still: true, Below: []gotermboard.Line{{said}},
	}
}

func (b *documentsBoard) stripLine(now time.Time, read documentsRead, records int) gotermboard.Line {
	line := gotermboard.Line{gotermboard.C("slate · documents", gotermboard.Bold), gotermboard.C("   ", "")}
	if total := read.list.TotalRecords; total != nil {
		counted := strconv.Itoa(records) + " records"
		if records < *total {
			counted = fmt.Sprintf("%d of %d records", records, *total)
		}
		line = append(line, gotermboard.C(fmt.Sprintf("%s · %d documents · newest written first", counted, len(read.list.Documents)), ""))
		if records < *total {
			line = append(line, gotermboard.C(" · "+boundNote(read.records), gotermboard.Yellow))
		}
		line = append(line, gotermboard.C("   ", ""))
	}
	// The body read ticks without reading while its body is current, so only its fault reaches
	// the strip, and the age is the list's.
	body := b.bodies.Status()
	body.ReadAt = time.Time{}
	return append(line, gotermboard.ReadStatus([]gotermboard.Status{b.list.Status(), body}, now)...)
}

// boundNote says what reads past the bound the read reached, which the count before it names.
func boundNote(records int) string {
	if records >= documentRecordsMost {
		return fmt.Sprintf("%d is the most one read answers", documentRecordsMost)
	}
	return fmt.Sprintf("+ reads %d more", documentRecordsStep)
}

// recordGroup is one record's documents, folded but for the record written to last. The title
// is a Span, so a long one wraps rather than widening the first column.
func (b *documentsBoard) recordGroup(record recordDocuments, folded bool, now time.Time) gotermboard.Group {
	entity := []gotermboard.EntityRef{recordEntity(record.record.Kind, record.record.Number)}
	rows := make([]gotermboard.Row, 0, len(record.documents))
	for _, document := range record.documents {
		rows = append(rows, b.documentRow(record.record, document, entity, now))
	}
	return gotermboard.Group{
		Key:      documentRecordKey(record.record.Number),
		Entities: entity,
		Title:    gotermboard.Line{gotermboard.C(strconv.Itoa(record.record.Number), gotermboard.Cyan)},
		Span:     gotermboard.Line{gotermboard.C(record.record.Title, gotermboard.Bold)},
		Folded:   folded,
		Rows:     rows,
	}
}

// documentRow draws a document's latest version on the columns. It names its record, so t goes
// to the record's tab, and / matches the record's title as well as what the row draws.
func (b *documentsBoard) documentRow(record api.RecordSummary, document api.ListedDocument, entity []gotermboard.EntityRef, now time.Time) gotermboard.Row {
	latest := document.Latest
	comments := gotermboard.C("·", dimCode)
	if document.CommentCount > 0 {
		comments = gotermboard.C(strconv.Itoa(document.CommentCount), gotermboard.Yellow)
	}
	return gotermboard.Row{
		Key:      documentKey(document.Address),
		Entities: entity,
		Cells: []gotermboard.Cell{
			gotermboard.C(document.Name, ""),
			gotermboard.C(document.Kind, dimCode),
			gotermboard.C(strconv.Itoa(latest.Version), ""),
			gotermboard.C(localClock(latest.WrittenTS, now), dimCode),
			gotermboard.C(orDash(strValue(latest.Worklog)), dimCode),
			comments,
		},
		Document:    b.documentRead(document),
		ReaderTitle: documentTitle(document),
		Text:        record.Title,
	}
}

// documentTitle is the address, kind and version a reader names, with when and where it was written.
func documentTitle(document api.ListedDocument) string {
	latest := document.Latest
	title := fmt.Sprintf("%s · %s · version %d · %s", document.Address, document.Kind, latest.Version, localClock(latest.WrittenTS, time.Now()))
	if worklog := strValue(latest.Worklog); worklog != "" {
		title += " · worklog " + worklog
	}
	return title
}

// documentRead is the document's body as last read, then its line comments. It reads the kept
// body each time it lays out, so a reader open on it redraws once the body lands.
func (b *documentsBoard) documentRead(listed api.ListedDocument) gotermboard.Document {
	return func(width int) []gotermboard.Line {
		width = min(width, readingWidth)
		var lines []gotermboard.Line
		fault := b.bodyFault.Load()
		if fault != nil && fault.address == listed.Address {
			lines = append(lines, gotermboard.Line{gotermboard.C("The latest version is unread: "+fault.fault, gotermboard.Red)}, gotermboard.Line{})
		}
		document, read := b.bodies.Value()[listed.Address]
		if !read {
			if fault == nil || fault.address != listed.Address {
				lines = append(lines, dimLine("reading "+listed.Address+"…"))
			}
			return lines
		}
		if shown := document.Version.Version; shown < listed.Latest.Version {
			lines = append(lines, dimLine(fmt.Sprintf("Version %d is being read, so this is version %d.", listed.Latest.Version, shown)), gotermboard.Line{})
		}
		lines = append(lines, b.markdown.Lines(document.Version.Body, width)...)
		return append(lines, b.commentLines(document, width)...)
	}
}

// commentIndent sets a comment's body under its line, past the line's number.
const commentIndent = "        "

// commentLines lists the line comments after the body, each quoting the line it sits on.
// Rendered markdown reflows, so a comment drawn beside a raw line number would sit beside the
// wrong text.
func (b *documentsBoard) commentLines(document api.Document, width int) []gotermboard.Line {
	if len(document.Comments) == 0 {
		return nil
	}
	count := len(document.Comments)
	lines := []gotermboard.Line{{}, heading(fmt.Sprintf("── %d line %s ──", count, plural(count, "comment", "comments")))}
	now := time.Now()
	for _, comment := range document.Comments {
		where, quoted := placedLine(document, comment)
		lines = append(lines, gotermboard.Line{
			gotermboard.C(where, gotermboard.Cyan),
			gotermboard.C("  "+orDash(strValue(comment.WrittenBy))+"  "+localClock(comment.WrittenTS, now), dimCode),
			gotermboard.C("   \""+quoted+"\"", ""),
		})
		for _, line := range b.markdown.Lines(comment.Body, max(width-len(commentIndent), 1)) {
			lines = append(lines, slices.Concat(gotermboard.Line{gotermboard.C(commentIndent, "")}, line))
		}
	}
	return lines
}

// placedLine is where the version read places the comment, and that line's text. Where that
// version changed or removed the line, it is the comment's own version and line, and the text
// there.
func placedLine(document api.Document, comment api.PlacedComment) (where, text string) {
	if shown := comment.ShownLine; shown != nil && *shown >= 1 && *shown <= len(document.Version.Lines) {
		return "line " + strconv.Itoa(*shown), strings.TrimSpace(document.Version.Lines[*shown-1])
	}
	if on := comment.On; on != nil {
		return fmt.Sprintf("line %d of version %d", on.Line, on.Version), strings.TrimSpace(on.Text)
	}
	return "on no line", ""
}

// documentsSnapshot is what --json prints, as tui-documents.schema.json describes it.
type documentsSnapshot struct {
	SchemaVersion  int                     `json:"schema_version"`
	TakenAt        time.Time               `json:"taken_at"`
	Tab            string                  `json:"tab"`
	RecordLimit    int                     `json:"record_limit"`
	TotalRecords   *int                    `json:"total_records"`
	Records        []documentsSnapshotItem `json:"records"`
	CursorDocument *cursorDocument         `json:"cursor_document"`
	Reads          []gotermboard.Status    `json:"reads"`
}

// cursorDocument is the cursor's document, as the reader draws it: the latest
// version and its line comments, under the names `slate documents show --json` gives them. The
// list of earlier versions stays out, as it does on the screen.
type cursorDocument struct {
	Record    api.RecordSummary   `json:"record"`
	Name      string              `json:"name"`
	Address   string              `json:"address"`
	Kind      string              `json:"kind"`
	CreatedTS time.Time           `json:"created_ts"`
	Version   api.DocumentVersion `json:"version"`
	Comments  []api.PlacedComment `json:"comments"`
}

// documentsSnapshotItem is one record and its documents, under the names that
// `slate documents list --json` gives them.
type documentsSnapshotItem struct {
	Record    api.RecordSummary     `json:"record"`
	Documents []api.DocumentSummary `json:"documents"`
}

// Snapshot is what --json prints of the tab.
func (b *documentsBoard) Snapshot(now time.Time) documentsSnapshot {
	read := b.list.Value()
	records := []documentsSnapshotItem{}
	for _, record := range byRecord(read.list.Documents) {
		item := documentsSnapshotItem{Record: record.record}
		for _, document := range record.documents {
			item.Documents = append(item.Documents, document.DocumentSummary)
		}
		records = append(records, item)
	}
	snapshot := documentsSnapshot{
		SchemaVersion: documentsSchemaVersion, TakenAt: now, Tab: tabDocuments,
		RecordLimit: cmp.Or(read.records, int(b.records.Load())), TotalRecords: read.list.TotalRecords,
		Records: records, Reads: []gotermboard.Status{b.list.Status(), b.bodies.Status()},
	}
	if key := b.cursor.Load(); key != nil {
		if document, read := b.bodies.Value()[strings.TrimPrefix(*key, documentKeyPrefix)]; read {
			snapshot.CursorDocument = &cursorDocument{
				Record: document.Record, Name: document.Name, Address: document.Address, Kind: document.Kind,
				CreatedTS: document.CreatedTS, Version: document.Version, Comments: listed(document.Comments),
			}
		}
	}
	return snapshot
}
