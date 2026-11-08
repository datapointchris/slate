package cli

import (
	"cmp"
	"context"
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

// How often the board reads. The poll is cheap by design, and its long text is most of
// what an initiative weighs, so that reads less often. The cursor's row reads again when
// the cursor lands on one it has not read, and otherwise at this pace.
const (
	boardReadEvery     = 15 * time.Second
	boardTextReadEvery = time.Minute
	cursorRowReadEvery = time.Minute
)

// The counts the scopes open at, and how far + and - move each.
const (
	defaultBoardPerTier = 5
	defaultBoardFirst   = 20
	boardPerTierStep    = 1
	boardFirstStep      = 5
)

// What the board shows of the initiatives it read.
type boardScopeKind int

const (
	scopePerTier boardScopeKind = iota
	scopeFirstInList
	scopeEveryInitiative
)

// boardScope is the kind showing and each counted kind's own count. Scopes cut in the
// client because the API's totals and tiers need every initiative whatever shows.
type boardScope struct {
	kind    boardScopeKind
	perTier int
	first   int
}

func (s boardScope) label() string {
	switch s.kind {
	case scopeFirstInList:
		return "first " + strconv.Itoa(s.first)
	case scopeEveryInitiative:
		return "all"
	}
	return "first " + strconv.Itoa(s.perTier) + " of each tier"
}

// moved is the scope with its count moved by steps, which stops at one step.
func (s boardScope) moved(steps int) boardScope {
	switch s.kind {
	case scopeFirstInList:
		s.first = max(s.first+steps*boardFirstStep, boardFirstStep)
	case scopePerTier:
		s.perTier = max(s.perTier+steps*boardPerTierStep, boardPerTierStep)
	}
	return s
}

// boardSort is one order within a tier. A nil compare keeps the order the API lists them
// in, which holds each pinned initiative first.
type boardSort struct {
	label   string
	compare func(a, b *api.BoardInitiative) int
}

var boardSorts = []boardSort{
	{label: "priority"},
	{label: "latest close", compare: func(a, b *api.BoardInitiative) int {
		return latestFirst(a.Closes.LastClosedTS, b.Closes.LastClosedTS)
	}},
	{label: "most open", compare: func(a, b *api.BoardInitiative) int { return cmp.Compare(b.OpenCount, a.OpenCount) }},
	{label: "newest", compare: func(a, b *api.BoardInitiative) int { return b.CreatedTS.Compare(a.CreatedTS) }},
}

func latestFirst(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	return b.Compare(*a)
}

// cursorRowReads is what only the cursor's row reads: an initiative's next issues and
// tokens, and an issue's whole description. Each is kept once read, so a row returned to
// draws at once.
type cursorRowReads struct {
	progress     map[int]api.InitiativeProgress
	descriptions map[int]string
}

// initiativesBoard is the initiatives tab. Its reads run off the program's goroutine and
// touch only their sources, the cursor and readFailure. The keys and Frame run on it.
type initiativesBoard struct {
	status      string
	scope       boardScope
	sort        int
	described   map[int]bool
	initiatives *gotermboard.Source[api.InitiativeBoard]
	text        *gotermboard.Source[api.InitiativeBoardText]
	cursorRow   *gotermboard.Source[cursorRowReads]
	cursor      atomic.Pointer[string]
	// markdown, records and recordsReadAt belong to the program's goroutine, with the keys and Frame.
	markdown      *markdown.Renderer
	records       map[int]bool
	recordsReadAt time.Time
	// readFailure is the board read's last failure, for --once and --json to exit on,
	// since a source keeps only its sentence.
	readFailure error
}

func newInitiativesBoard(client *api.Client, status string, scope boardScope) *initiativesBoard {
	zone := issueZone()
	b := &initiativesBoard{status: status, scope: scope, described: map[int]bool{}}
	b.markdown = markdown.New(b.colorRecordNumbers)
	b.initiatives = gotermboard.NewSource("initiatives", boardReadEvery, func(ctx context.Context) (api.InitiativeBoard, string, error) {
		read, err := client.ReadInitiativeBoard(ctx, status, zone)
		b.readFailure = err
		return read, "", err
	})
	b.text = gotermboard.NewSource("long text", boardTextReadEvery, func(ctx context.Context) (api.InitiativeBoardText, string, error) {
		read, err := client.ReadInitiativeBoardText(ctx, status)
		return read, "", err
	})
	b.cursorRow = gotermboard.NewSource("cursor's row", cursorRowReadEvery, func(ctx context.Context) (cursorRowReads, string, error) {
		read, err := b.readCursorRow(ctx, client, zone)
		return read, "", err
	})
	return b
}

// Sources is gotermboard.Board's.
func (b *initiativesBoard) Sources() []gotermboard.Refresher {
	return []gotermboard.Refresher{b.initiatives, b.text, b.cursorRow}
}

// Groupings is gotermboard.Board's. The tiers are the one grouping.
func (b *initiativesBoard) Groupings() []string { return nil }

// CursorOn is gotermboard.CursorBoard's. The read waits for the next tick, so holding a
// key down reads only the row it stops on.
func (b *initiativesBoard) CursorOn(key string) {
	b.cursor.Store(&key)
	if !b.cursorRow.Value().holds(key) {
		b.cursorRow.Force()
	}
}

func (r cursorRowReads) holds(key string) bool {
	if number, ok := numberFromKey(key, initiativeKeyPrefix); ok {
		_, held := r.progress[number]
		return held
	}
	if number, ok := numberFromKey(key, issueKeyPrefix); ok {
		_, held := r.descriptions[number]
		return held
	}
	return true
}

// readCursorRow reads what the row under the cursor shows beyond the poll, keeping the
// rows already read. A window ending at the read leaves out the closes a board already
// has counts of, which are most of what a progress read weighs.
func (b *initiativesBoard) readCursorRow(ctx context.Context, client *api.Client, zone api.DayZone) (cursorRowReads, error) {
	held := b.cursorRow.Value()
	next := cursorRowReads{progress: maps.Clone(held.progress), descriptions: maps.Clone(held.descriptions)}
	if next.progress == nil {
		next.progress, next.descriptions = map[int]api.InitiativeProgress{}, map[int]string{}
	}
	key := ""
	if at := b.cursor.Load(); at != nil {
		key = *at
	}
	if number, ok := numberFromKey(key, initiativeKeyPrefix); ok {
		window := api.Window{Since: time.Now()}
		progress, err := client.ListInitiativesProgress(ctx, []string{strconv.Itoa(number)}, window, zone)
		if err != nil {
			return held, err
		}
		if len(progress) > 0 {
			next.progress[number] = progress[0]
		}
	}
	if number, ok := numberFromKey(key, issueKeyPrefix); ok {
		issue, err := client.GetIssue(ctx, strconv.Itoa(number), zone)
		if err != nil {
			return held, err
		}
		next.descriptions[number] = strValue(issue.Description)
	}
	return next, nil
}

// readOnce is every read a printed frame draws from, the cursor's row last because the
// first read says which row that is.
func (b *initiativesBoard) readOnce(ctx context.Context) error {
	gotermboard.RefreshAll(ctx, b.initiatives, b.text)
	if b.readFailure != nil {
		return b.readFailure
	}
	opening := b.openingKey(b.initiatives.Value())
	b.cursor.Store(&opening)
	b.cursorRow.Refresh(ctx)
	return nil
}

// orderedAndShown is the initiatives in the order the sort leaves them, and the numbers
// the scope shows.
func (b *initiativesBoard) orderedAndShown(read api.InitiativeBoard) ([]api.BoardInitiative, map[int]bool) {
	ordered := slices.Clone(read.Initiatives)
	if compare := boardSorts[b.sort].compare; compare != nil {
		tierAt := func(priority int) int {
			return slices.IndexFunc(read.Tiers, func(tier api.BoardTier) bool { return tier.Priority == priority })
		}
		slices.SortStableFunc(ordered, func(x, y api.BoardInitiative) int {
			return cmp.Or(cmp.Compare(tierAt(x.Priority), tierAt(y.Priority)), compare(&x, &y))
		})
	}
	shown := make(map[int]bool, len(ordered))
	seen := map[int]int{}
	for at, initiative := range ordered {
		switch b.scope.kind {
		case scopePerTier:
			shown[initiative.Number] = seen[initiative.Priority] < b.scope.perTier
			seen[initiative.Priority]++
		case scopeFirstInList:
			shown[initiative.Number] = at < b.scope.first
		case scopeEveryInitiative:
			shown[initiative.Number] = true
		}
	}
	return ordered, shown
}

// openingKey is the first initiative showing, in the tiers' order.
func (b *initiativesBoard) openingKey(read api.InitiativeBoard) string {
	ordered, shown := b.orderedAndShown(read)
	for _, tier := range read.Tiers {
		for _, initiative := range ordered {
			if initiative.Priority == tier.Priority && shown[initiative.Number] {
				return initiativeKey(initiative.Number)
			}
		}
	}
	return ""
}

// Actions is gotermboard.Keyed's.
func (b *initiativesBoard) Actions() []gotermboard.Action {
	scoped := func(kind boardScopeKind) func(*gotermboard.Model) tea.Cmd {
		return func(m *gotermboard.Model) tea.Cmd {
			b.scope.kind = kind
			m.Rebuild()
			return nil
		}
	}
	counted := func(steps int) func(*gotermboard.Model) tea.Cmd {
		return func(m *gotermboard.Model) tea.Cmd {
			b.scope = b.scope.moved(steps)
			m.Rebuild()
			return nil
		}
	}
	hasCount := func(*gotermboard.Model) bool { return b.scope.kind != scopeEveryInitiative }
	return []gotermboard.Action{
		{Keys: []string{"1"}, Label: "show the first of each priority tier", Footer: "1/2/3 scope", Run: scoped(scopePerTier)},
		{Keys: []string{"2"}, Label: "show the first in list order", Run: scoped(scopeFirstInList)},
		{Keys: []string{"3"}, Label: "show every initiative", Run: scoped(scopeEveryInitiative)},
		{Keys: []string{"+", "="}, Label: "show more of them", Footer: "+/- count", Run: counted(1), Applies: hasCount},
		{Keys: []string{"-"}, Label: "show fewer of them", Run: counted(-1), Applies: hasCount},
		{
			Keys: []string{"enter"}, Label: "unfold an initiative's issues, fold a group, or open an issue's description, full screen where it runs past the screen",
			Footer: "enter issues", Run: (*gotermboard.Model).Toggle,
		},
		{
			Keys: []string{"d"}, Label: "read the initiative's description, full screen where it runs past the screen", Footer: "d description",
			Run: func(m *gotermboard.Model) tea.Cmd {
				number, ok := numberFromKey(m.Cursor(), initiativeKeyPrefix)
				if !ok || b.readTallDescription(m, number) {
					return nil
				}
				b.described[number] = !b.described[number]
				m.Rebuild()
				m.Reveal()
				return nil
			},
			Applies: func(m *gotermboard.Model) bool { return strings.HasPrefix(m.Cursor(), initiativeKeyPrefix) },
		},
		{
			Keys: []string{"s"}, Label: "cycle the sort within a tier", Footer: "s sort",
			Run: func(m *gotermboard.Model) tea.Cmd {
				b.sort = (b.sort + 1) % len(boardSorts)
				m.Rebuild()
				return nil
			},
		},
	}
}

// initiativesSnapshot is what --json prints: every read the frame is drawn from, and what
// the scope and the sort make of them.
type initiativesSnapshot struct {
	Tab       string                  `json:"tab"`
	Status    string                  `json:"status"`
	Scope     string                  `json:"scope"`
	Sort      string                  `json:"sort"`
	Shown     []int                   `json:"shown"`
	Reads     []gotermboard.Status    `json:"reads"`
	Board     api.InitiativeBoard     `json:"board"`
	Text      api.InitiativeBoardText `json:"text"`
	CursorRow *api.InitiativeProgress `json:"cursor_row"`
}

func (b *initiativesBoard) snapshot() initiativesSnapshot {
	read := b.initiatives.Value()
	ordered, shown := b.orderedAndShown(read)
	numbers := []int{}
	for _, initiative := range ordered {
		if shown[initiative.Number] {
			numbers = append(numbers, initiative.Number)
		}
	}
	snapshot := initiativesSnapshot{
		Tab: tabInitiatives, Status: cmp.Or(b.status, "active"), Scope: b.scope.label(), Sort: boardSorts[b.sort].label,
		Shown: numbers, Board: read, Text: b.text.Value(),
		Reads: []gotermboard.Status{b.initiatives.Status(), b.text.Status(), b.cursorRow.Status()},
	}
	if number, ok := numberFromKey(b.openingKey(read), initiativeKeyPrefix); ok {
		if progress, held := b.cursorRow.Value().progress[number]; held {
			snapshot.CursorRow = &progress
		}
	}
	return snapshot
}

// The keys of the rows a read follows.
const (
	initiativeKeyPrefix = "initiative:"
	issueKeyPrefix      = "issue:"
)

func initiativeKey(number int) string { return initiativeKeyPrefix + strconv.Itoa(number) }

func issueKey(number int) string { return issueKeyPrefix + strconv.Itoa(number) }

func numberFromKey(key, prefix string) (int, bool) {
	text, found := strings.CutPrefix(key, prefix)
	if !found {
		return 0, false
	}
	number, err := strconv.Atoi(text)
	return number, err == nil
}
