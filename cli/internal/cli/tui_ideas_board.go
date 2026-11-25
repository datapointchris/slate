package cli

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/gotermboard"
	"github.com/datapointchris/gotermboard/markdown"

	"github.com/datapointchris/slate/cli/internal/api"
)

const tabIdeas = "ideas"

// ideasSchemaVersion is the shape tui-ideas.schema.json describes, which --json prints.
const ideasSchemaVersion = 2

// noneMark stands in a cell for a pursuit or tag the idea has none of.
const noneMark = "—"

// ideaColumns hold the number, priority, created day, pursuits, tags and refs count, then the
// title, which the screen's edge cuts and f shows whole under the row.
var ideaColumns = []gotermboard.Column{
	{Title: ""},
	{Title: "PRIORITY"},
	{Title: "CREATED"},
	{Title: "PURSUIT", Max: 28},
	{Title: "TAG", Max: 20},
	{Title: "REFS", Right: true},
	{Title: "TITLE"},
}

// ideasBoard is the ideas tab: every open idea, newest first. Its read runs off the
// program's goroutine and touches only its source and readFailure. The keys and Frame run on it.
type ideasBoard struct {
	ideas    *gotermboard.Source[[]api.Idea]
	markdown *markdown.Renderer
	// pursuit and tag narrow the rows, and nil and empty narrow nothing. Each is kept by value,
	// so a read that drops it from the choices keeps it in force until the key moves on.
	pursuit *api.PursuitSummary
	tag     string
	// readFailure is the read's last failure, for --once and --json to exit on.
	readFailure error
}

func newIdeasBoard(client *api.Client) *ideasBoard {
	b := &ideasBoard{markdown: markdown.New(nil)}
	b.ideas = gotermboard.NewSource("ideas", boardTextReadEvery, func(ctx context.Context) ([]api.Idea, string, error) {
		read, err := client.ListIdeas(ctx, api.IdeaFilter{}, nil)
		b.readFailure = err
		return read, "", err
	})
	return b
}

// Sources is gotermboard.Board's.
func (b *ideasBoard) Sources() []gotermboard.Refresher { return []gotermboard.Refresher{b.ideas} }

// Groupings is gotermboard.Board's.
func (b *ideasBoard) Groupings() []string { return nil }

func (b *ideasBoard) readOnce(ctx context.Context) error {
	gotermboard.RefreshAll(ctx, b.ideas)
	return b.readFailure
}

// EntityKinds is gotermboard.EntityBoard's.
func (b *ideasBoard) EntityKinds() []string { return []string{entityKindIdea} }

// ShowEntity is gotermboard.EntityBoard's. A pursuit or tag narrowing that hides the idea lifts,
// as the seek clears a filter that hides it, so the tab says it holds no idea only where it
// holds none.
func (b *ideasBoard) ShowEntity(ref gotermboard.EntityRef) bool {
	number, ok := soughtNumber(&ref, entityKindIdea)
	if !ok {
		return false
	}
	if at := slices.IndexFunc(b.ideas.Value(), func(idea api.Idea) bool { return idea.Number == number }); at >= 0 && !b.holds(b.ideas.Value()[at]) {
		b.pursuit, b.tag = nil, ""
	}
	return false
}

// openNewestFirst leaves out each idea that became other work, whose rows are that work's
// now, and puts the newest first.
func openNewestFirst(read []api.Idea) []api.Idea {
	ideas := slices.DeleteFunc(slices.Clone(read), func(idea api.Idea) bool { return idea.Became != nil })
	slices.SortStableFunc(ideas, func(a, b api.Idea) int {
		return cmp.Or(b.CreatedTS.Compare(a.CreatedTS), cmp.Compare(b.Number, a.Number))
	})
	return ideas
}

// holds says the idea is in the pursuit and carries the tag that narrow the tab.
func (b *ideasBoard) holds(idea api.Idea) bool {
	if b.pursuit != nil && !slices.ContainsFunc(idea.Pursuits, func(p api.PursuitSummary) bool { return p.Number == b.pursuit.Number }) {
		return false
	}
	return b.tag == "" || slices.Contains(idea.Tags, b.tag)
}

// shown is the open ideas newest first, and those the narrowing leaves.
func (b *ideasBoard) shown() (open, narrowed []api.Idea) {
	open = openNewestFirst(b.ideas.Value())
	for _, idea := range open {
		if b.holds(idea) {
			narrowed = append(narrowed, idea)
		}
	}
	return open, narrowed
}

// pursuitChoices is every pursuit that holds an open idea, by name.
func pursuitChoices(ideas []api.Idea) []api.PursuitSummary {
	var pursuits []api.PursuitSummary
	for _, idea := range ideas {
		for _, pursuit := range idea.Pursuits {
			if !slices.ContainsFunc(pursuits, func(p api.PursuitSummary) bool { return p.Number == pursuit.Number }) {
				pursuits = append(pursuits, pursuit)
			}
		}
	}
	slices.SortFunc(pursuits, func(a, b api.PursuitSummary) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.Number, b.Number))
	})
	return pursuits
}

// tagChoices is every tag that an open idea carries, by name.
func tagChoices(ideas []api.Idea) []string {
	var tags []string
	for _, idea := range ideas {
		tags = append(tags, idea.Tags...)
	}
	slices.Sort(tags)
	return slices.Compact(tags)
}

// nextChoice is the choice after at, or -1 for all once past the last. An at of -1, for all
// or for a narrowing missing from the choices, moves to the first.
func nextChoice(at, count int) int {
	if at+1 >= count {
		return -1
	}
	return at + 1
}

// Actions is gotermboard.Keyed's.
func (b *ideasBoard) Actions() []gotermboard.Action {
	return []gotermboard.Action{
		{
			Keys: []string{"u"}, Label: "narrow to the next pursuit that holds an idea, and after the last to every pursuit", Footer: "u pursuit",
			Run: func(m *gotermboard.Model) tea.Cmd {
				open, _ := b.shown()
				choices := pursuitChoices(open)
				at := -1
				if b.pursuit != nil {
					at = slices.IndexFunc(choices, func(p api.PursuitSummary) bool { return p.Number == b.pursuit.Number })
				}
				b.pursuit = nil
				if next := nextChoice(at, len(choices)); next >= 0 {
					b.pursuit = &choices[next]
				}
				m.Rebuild()
				return nil
			},
		},
		{
			Keys: []string{"#"}, Label: "narrow to the next tag that an idea carries, and after the last to every tag", Footer: "# tag",
			Run: func(m *gotermboard.Model) tea.Cmd {
				open, _ := b.shown()
				choices := tagChoices(open)
				next := nextChoice(slices.Index(choices, b.tag), len(choices))
				b.tag = ""
				if next >= 0 {
					b.tag = choices[next]
				}
				m.Rebuild()
				return nil
			},
		},
	}
}

func (b *ideasBoard) pursuitLabel() string {
	if b.pursuit == nil {
		return "all"
	}
	return b.pursuit.Name
}

func (b *ideasBoard) tagLabel() string { return cmp.Or(b.tag, "all") }

// narrowingCode draws a narrowing in force so it reads apart from all.
func narrowingCode(narrows bool) string {
	if narrows {
		return gotermboard.BoldYellow
	}
	return ""
}

// Frame is gotermboard.Board's.
func (b *ideasBoard) Frame(now time.Time, _ string) gotermboard.Frame {
	frame := gotermboard.Frame{Columns: ideaColumns}
	if b.ideas.Status().ReadAt.IsZero() {
		frame.Strip = []gotermboard.Line{b.stripLine(now, 0)}
		return frame
	}
	open, narrowed := b.shown()
	frame.Strip = []gotermboard.Line{b.stripLine(now, len(open)), {}}
	title := strconv.Itoa(len(open)) + " ideas"
	if b.pursuit != nil || b.tag != "" {
		title = fmt.Sprintf("%d of %d ideas", len(narrowed), len(open))
	}
	group := gotermboard.Group{
		Key:     "ideas",
		Title:   gotermboard.Line{gotermboard.C(title, tierTone)},
		Heading: true,
		Still:   true,
	}
	for _, idea := range narrowed {
		group.Rows = append(group.Rows, b.ideaRow(idea))
	}
	if len(narrowed) == 0 {
		group.Below = []gotermboard.Line{dimLine(b.nothingShown())}
	}
	frame.Groups = []gotermboard.Group{group}
	return frame
}

func (b *ideasBoard) stripLine(now time.Time, open int) gotermboard.Line {
	line := gotermboard.Line{
		gotermboard.C("slate · ideas", gotermboard.Bold),
		gotermboard.C("   "+now.Format("15:04:05")+"   ", ""),
		gotermboard.C(fmt.Sprintf("%d open · newest first   ", open), ""),
		gotermboard.C("pursuit ", dimCode),
		gotermboard.C(b.pursuitLabel(), narrowingCode(b.pursuit != nil)),
		gotermboard.C("  tag ", dimCode),
		gotermboard.C(b.tagLabel(), narrowingCode(b.tag != "")),
		gotermboard.C("   ", ""),
	}
	return append(line, gotermboard.ReadStatus([]gotermboard.Status{b.ideas.Status()}, now)...)
}

// nothingShown says why the table is empty, in the narrowing's own words.
func (b *ideasBoard) nothingShown() string {
	switch {
	case b.pursuit != nil && b.tag != "":
		return "No open idea in " + b.pursuit.Name + " carries " + b.tag + "."
	case b.pursuit != nil:
		return "No open idea is in " + b.pursuit.Name + "."
	case b.tag != "":
		return "No open idea carries " + b.tag + "."
	}
	return "No open idea."
}

func ideaKey(number int) string { return "idea:" + strconv.Itoa(number) }

func pursuitNames(pursuits []api.PursuitSummary) string {
	names := make([]string, len(pursuits))
	for at, pursuit := range pursuits {
		names[at] = pursuit.Name
	}
	return cmp.Or(strings.Join(names, ", "), noneMark)
}

// ideaRow draws an idea on the columns. Enter opens its read, and / matches its description.
func (b *ideasBoard) ideaRow(idea api.Idea) gotermboard.Row {
	refs := gotermboard.C("·", dimCode)
	if count := len(idea.Refs); count > 0 {
		refs = gotermboard.C(strconv.Itoa(count), "")
	}
	return gotermboard.Row{
		Key:      ideaKey(idea.Number),
		Entities: recordAndPursuits(entityKindIdea, idea.Number, idea.Pursuits),
		Cells: []gotermboard.Cell{
			gotermboard.C(strconv.Itoa(idea.Number), gotermboard.Cyan),
			gotermboard.C(priorityName(idea.Priority), tierCode(idea.Priority)),
			gotermboard.C(idea.CreatedTS.Local().Format(time.DateOnly), dimCode),
			gotermboard.C(pursuitNames(idea.Pursuits), ""),
			gotermboard.C(cmp.Or(strings.Join(idea.Tags, ", "), noneMark), ""),
			refs,
			gotermboard.C(idea.Title, gotermboard.Bold),
		},
		Under:         gotermboard.C(idea.Title, ""),
		UnderWhereCut: true,
		Document:      b.ideaRead(idea),
		ReaderTitle:   "idea " + strconv.Itoa(idea.Number),
		Text:          strValue(idea.Description),
	}
}

// ideaRead is the idea whole: its placements, its refs oldest first and its description.
func (b *ideasBoard) ideaRead(idea api.Idea) gotermboard.Document {
	return func(width int) []gotermboard.Line {
		width = min(width, readingWidth)
		lines := slices.Concat(b.markdown.Lines("# "+idea.Title, width), []gotermboard.Line{
			{
				gotermboard.C("priority ", dimCode), gotermboard.C(priorityName(idea.Priority), tierCode(idea.Priority)),
				gotermboard.C("   created ", dimCode), gotermboard.C(idea.CreatedTS.Local().Format(time.DateOnly), ""),
				gotermboard.C("   updated ", dimCode), gotermboard.C(idea.UpdatedTS.Local().Format(time.DateOnly), ""),
			},
			{gotermboard.C("pursuits ", dimCode), gotermboard.C(pursuitNames(idea.Pursuits), "")},
			{gotermboard.C("tags     ", dimCode), gotermboard.C(cmp.Or(strings.Join(idea.Tags, ", "), noneMark), "")},
		})
		if len(idea.Refs) > 0 {
			lines = append(lines, gotermboard.Line{}, heading("Refs · "+strconv.Itoa(len(idea.Refs))))
			for _, ref := range idea.Refs {
				line := gotermboard.Line{gotermboard.C(ref.Ref, gotermboard.Cyan), gotermboard.C("  "+ref.Kind, dimCode)}
				if note := strValue(ref.Note); note != "" {
					line = append(line, gotermboard.C("  "+note, ""))
				}
				lines = append(lines, line)
			}
		}
		lines = append(lines, gotermboard.Line{})
		if description := strValue(idea.Description); strings.TrimSpace(description) != "" {
			return append(lines, b.markdown.Lines(description, width)...)
		}
		return append(lines, dimLine("No description."))
	}
}

// ideasSnapshot is what --json prints, as tui-ideas.schema.json describes it.
type ideasSnapshot struct {
	SchemaVersion int                  `json:"schema_version"`
	TakenAt       time.Time            `json:"taken_at"`
	Tab           string               `json:"tab"`
	Pursuit       *api.PursuitSummary  `json:"pursuit"`
	Tag           *string              `json:"tag"`
	Open          int                  `json:"open"`
	Rows          []ideaSnapshotRow    `json:"rows"`
	Reads         []gotermboard.Status `json:"reads"`
}

// ideaSnapshotRow is one idea the tab lists, under the names `slate ideas list --json` gives
// it, and the count of its refs that the row draws.
type ideaSnapshotRow struct {
	Number      int                  `json:"number"`
	Title       string               `json:"title"`
	Description *string              `json:"description"`
	Priority    int                  `json:"priority"`
	CreatedTS   time.Time            `json:"created_ts"`
	UpdatedTS   time.Time            `json:"updated_ts"`
	Pursuits    []api.PursuitSummary `json:"pursuits"`
	Tags        []string             `json:"tags"`
	Refs        []api.RecordRef      `json:"refs"`
	RefCount    int                  `json:"ref_count"`
	// Became is null on every row, because the tab lists only the ideas still open.
	Became *api.Became `json:"became"`
}

// Snapshot is what --json prints of the tab.
func (b *ideasBoard) Snapshot(now time.Time) ideasSnapshot {
	open, narrowed := b.shown()
	rows := make([]ideaSnapshotRow, 0, len(narrowed))
	for _, idea := range narrowed {
		rows = append(rows, ideaSnapshotRow{
			Number: idea.Number, Title: idea.Title, Description: idea.Description, Priority: idea.Priority,
			CreatedTS: idea.CreatedTS, UpdatedTS: idea.UpdatedTS,
			Pursuits: listed(idea.Pursuits), Tags: listed(idea.Tags), Refs: listed(idea.Refs), RefCount: len(idea.Refs),
			Became: idea.Became,
		})
	}
	snapshot := ideasSnapshot{
		SchemaVersion: ideasSchemaVersion, TakenAt: now, Tab: tabIdeas, Pursuit: b.pursuit,
		Open: len(open), Rows: rows, Reads: []gotermboard.Status{b.ideas.Status()},
	}
	if b.tag != "" {
		snapshot.Tag = &b.tag
	}
	return snapshot
}

// listed prints a nil list as [], where the schema asks for an array.
func listed[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}
