package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/gotermboard"
	"github.com/datapointchris/gotermboard/chart"

	"github.com/datapointchris/slate/cli/internal/api"
)

const tabPursuits = "pursuits"

// pursuitsSchemaVersion is the shape tui-pursuits.schema.json describes, which --json prints.
const pursuitsSchemaVersion = 1

const (
	// usageChartHeight is a panel chart's lines, its time axis included.
	usageChartHeight = 10
	// totalChartMost is how many pursuits the total's chart draws apart where no focus names
	// any, the most used on the last day, with every other drawn as the rest.
	totalChartMost = 5
	// statementShown caps a statement on the strip, in runes. The total's panel draws it whole.
	statementShown = 72
)

// Each group that holds a pursuit's row, as --json names it.
const (
	pursuitGroupNamed = "named"
	pursuitGroupRest  = "rest"
	pursuitGroupEvery = "every"
)

// totalLayerCodes color the total chart's pursuits in turn, and the rest draws in restLayerCode.
var (
	totalLayerCodes = []string{gotermboard.Cyan, gotermboard.Green, gotermboard.Yellow, gotermboard.Magenta, gotermboard.Blue, gotermboard.Red}
	restLayerCode   = gotermboard.White
)

var pursuitColumns = []gotermboard.Column{
	{Title: "#"},
	{Title: "SHARE"},
	{Title: "USAGE"},
	{Title: "FACTOR"},
	{Title: "OUTPUT", Right: true},
	{Title: "INIT", Right: true},
	{Title: "IDEAS", Right: true},
	{Title: "LAST WORKED"},
	{Title: "NAME"},
}

// usageRead is one read of the usage by day, with the id of the focus it asked for, 0 for the
// one in force.
type usageRead struct {
	asked int
	usage api.FocusUsage
}

// pursuitsBoard is the pursuits tab: the focus's standing, one row per pursuit, each charted by
// day in the panel. Its reads run off the program's goroutine and touch only their sources, the
// atomic and their own failure. The keys and Frame run on it.
type pursuitsBoard struct {
	standing *gotermboard.Source[api.FocusStanding]
	pursuits *gotermboard.Source[[]api.Pursuit]
	history  *gotermboard.Source[[]api.Focus]
	usage    *gotermboard.Source[usageRead]
	// viewing is the id of the earlier focus that h moved to, and 0 for the one in force.
	viewing atomic.Int64
	// Each read's last failure, for --once and --json to exit on.
	standingFailure, pursuitsFailure, historyFailure, usageFailure error
}

func newPursuitsBoard(client *api.Client) *pursuitsBoard {
	zone := issueZone()
	b := &pursuitsBoard{}
	b.standing = gotermboard.NewSource("focus", boardReadEvery, func(ctx context.Context) (api.FocusStanding, string, error) {
		standing, err := client.GetFocusStanding(ctx, zone)
		b.standingFailure = err
		if err == nil {
			b.forceUsageWhereStale(standing)
		}
		return standing, "", err
	})
	b.pursuits = gotermboard.NewSource("pursuits", boardTextReadEvery, func(ctx context.Context) ([]api.Pursuit, string, error) {
		pursuits, err := client.ListPursuits(ctx, "all", nil)
		b.pursuitsFailure = err
		return pursuits, "", err
	})
	b.history = gotermboard.NewSource("focus history", boardTextReadEvery, func(ctx context.Context) ([]api.Focus, string, error) {
		focuses, err := client.ListFocuses(ctx, zone, nil)
		b.historyFailure = err
		return focuses, "", err
	})
	b.usage = gotermboard.NewSource("usage by day", boardTextReadEvery, func(ctx context.Context) (usageRead, string, error) {
		asked := int(b.viewing.Load())
		var id *int
		if asked != 0 {
			id = &asked
		}
		usage, err := focusUsageRead(ctx, client, id)
		b.usageFailure = err
		return usageRead{asked: asked, usage: usage}, "", err
	})
	return b
}

// forceUsageWhereStale reads the usage again where the tab shows the focus in force and the
// usage held is another focus's, as once a focus is set or ends.
func (b *pursuitsBoard) forceUsageWhereStale(standing api.FocusStanding) {
	held := b.usage.Value()
	if b.viewing.Load() == 0 && !b.usage.Status().ReadAt.IsZero() && held.asked == 0 && focusID(held.usage.Focus) != focusID(standing.Focus) {
		b.usage.Force()
	}
}

// focusID is the focus's id, and 0 for none.
func focusID(focus *api.Focus) int {
	if focus == nil {
		return 0
	}
	return focus.ID
}

// Sources is gotermboard.Board's.
func (b *pursuitsBoard) Sources() []gotermboard.Refresher {
	return []gotermboard.Refresher{b.standing, b.pursuits, b.history, b.usage}
}

// Groupings is gotermboard.Board's.
func (b *pursuitsBoard) Groupings() []string { return nil }

func (b *pursuitsBoard) readOnce(ctx context.Context) error {
	gotermboard.RefreshAll(ctx, b.Sources()...)
	return errors.Join(b.standingFailure, b.pursuitsFailure, b.historyFailure, b.usageFailure)
}

// EntityKinds is gotermboard.EntityBoard's.
func (b *pursuitsBoard) EntityKinds() []string { return []string{entityKindPursuit} }

// ShowEntity is gotermboard.EntityBoard's. A line on another tab names a pursuit as it serves the
// work now, so a jump shows the focus in force.
func (b *pursuitsBoard) ShowEntity(ref gotermboard.EntityRef) bool {
	if _, ok := soughtNumber(&ref, entityKindPursuit); ok && b.viewing.Swap(0) != 0 {
		b.usage.Force()
	}
	return false
}

// earlier is every focus but the one in force, the latest set first, as h steps back through them.
func (b *pursuitsBoard) earlier() []api.Focus {
	currentID := focusID(b.standing.Value().Focus)
	var focuses []api.Focus
	for _, focus := range b.history.Value() {
		if focus.ID != currentID {
			focuses = append(focuses, focus)
		}
	}
	return focuses
}

// stepped is the focus steps along from the one shown, an earlier one where steps is positive,
// as its id, 0 for the one in force. It says false where no focus lies that far.
func (b *pursuitsBoard) stepped(steps int) (int, bool) {
	earlier := b.earlier()
	at := -1
	if viewing := int(b.viewing.Load()); viewing != 0 {
		at = slices.IndexFunc(earlier, func(focus api.Focus) bool { return focus.ID == viewing })
		// Where the history lacks the focus shown, l returns to the one in force.
		if at < 0 {
			return 0, steps < 0
		}
	}
	next := at + steps
	switch {
	case next < -1 || next >= len(earlier):
		return 0, false
	case next == -1:
		return 0, true
	}
	return earlier[next].ID, true
}

// Actions is gotermboard.Keyed's.
func (b *pursuitsBoard) Actions() []gotermboard.Action {
	step := func(steps int) func(*gotermboard.Model) tea.Cmd {
		return func(m *gotermboard.Model) tea.Cmd {
			id, ok := b.stepped(steps)
			if !ok {
				return nil
			}
			b.viewing.Store(int64(id))
			b.usage.Force()
			m.Rebuild()
			return nil
		}
	}
	can := func(steps int) func(*gotermboard.Model) bool {
		return func(*gotermboard.Model) bool {
			_, ok := b.stepped(steps)
			return ok
		}
	}
	return []gotermboard.Action{
		{Keys: []string{"h"}, Label: "show the focus set before the one shown", Footer: "h/l focus", Run: step(1), Applies: can(1)},
		{Keys: []string{"l"}, Label: "show the focus set after the one shown, up to the one in force", Run: step(-1), Applies: can(-1)},
	}
}

// standingRow is one pursuit as a row draws it: its figures, and its name, status and last work
// now.
type standingRow struct {
	api.PursuitFigures
	name, status string
	touched      *time.Time
}

// pursuitsView is what the tab shows: the focus in force from the standing, or an earlier focus
// from its usage read's last day.
type pursuitsView struct {
	viewing int
	focus   *api.Focus
	// read says the rows' read has landed.
	read bool
	// rows are in the standing's order, the furthest under its share first.
	rows   []standingRow
	window api.FocusWindow
	// usage is the usage by day of the focus shown, nil until it is read, and days each
	// pursuit's figures on each of its days.
	usage *api.FocusUsage
	days  map[int][]*api.PursuitUsageOnDay
	// pursuits is each pursuit as GET /pursuits reads it, by number.
	pursuits map[int]api.Pursuit
}

func (b *pursuitsBoard) view() pursuitsView {
	standing := b.standing.Value()
	v := pursuitsView{viewing: int(b.viewing.Load()), pursuits: map[int]api.Pursuit{}}
	for _, pursuit := range b.pursuits.Value() {
		v.pursuits[pursuit.Number] = pursuit
	}
	held := b.usage.Value()
	if !b.usage.Status().ReadAt.IsZero() && held.asked == v.viewing && (v.viewing != 0 || focusID(held.usage.Focus) == focusID(standing.Focus)) {
		v.usage = &held.usage
		v.days = map[int][]*api.PursuitUsageOnDay{}
		for _, pursuit := range byPursuit(held.usage) {
			v.days[pursuit.pursuit.Number] = pursuit.days
		}
	}
	if v.viewing == 0 {
		v.focus, v.window, v.read = standing.Focus, standing.Window, !b.standing.Status().ReadAt.IsZero()
		for _, pursuit := range standing.Pursuits {
			v.rows = append(v.rows, standingRow{PursuitFigures: pursuit.PursuitFigures, name: pursuit.Name, status: pursuit.Status, touched: pursuit.TouchedTS})
		}
		return v
	}
	if at := slices.IndexFunc(b.history.Value(), func(focus api.Focus) bool { return focus.ID == v.viewing }); at >= 0 {
		v.focus = &b.history.Value()[at]
	}
	if v.usage == nil || len(v.usage.Days) == 0 {
		return v
	}
	v.read = true
	if v.usage.Focus != nil {
		v.focus = v.usage.Focus
	}
	v.window = v.usage.Days[len(v.usage.Days)-1].Window
	touched := map[int]*time.Time{}
	for _, pursuit := range standing.Pursuits {
		touched[pursuit.Number] = pursuit.TouchedTS
	}
	for _, pursuit := range byPursuit(*v.usage) {
		if last := pursuit.last(); last != nil {
			v.rows = append(v.rows, standingRow{PursuitFigures: last.PursuitFigures, name: pursuit.pursuit.Name, status: pursuit.pursuit.Status, touched: touched[last.Number]})
		}
	}
	return v
}

// Frame is gotermboard.Board's.
func (b *pursuitsBoard) Frame(now time.Time, _ string) gotermboard.Frame {
	frame := gotermboard.Frame{Columns: pursuitColumns}
	view := b.view()
	frame.Strip = []gotermboard.Line{b.stripLine(now, view), {}}
	if !view.read && view.viewing == 0 {
		frame.Strip = frame.Strip[:1]
		if fault := b.standing.Status().Fault; fault != "" {
			frame.Strip = append(frame.Strip, gotermboard.Line{})
			frame.Groups = []gotermboard.Group{{
				Key: "focus", Title: gotermboard.Line{gotermboard.C("pursuits", tierTone)}, Heading: true, Still: true,
				Below: []gotermboard.Line{{gotermboard.C("The focus is unread: "+fault, gotermboard.Red)}},
			}}
		}
		return frame
	}
	frame.Groups = []gotermboard.Group{b.totalGroup(view, now)}
	if view.read {
		for _, group := range pursuitGroups(view) {
			frame.Groups = append(frame.Groups, b.pursuitGroup(view, group.key, group.title, group.rows, now))
		}
	}
	if len(frame.Groups) > 1 && len(frame.Groups[1].Rows) > 0 {
		frame.StartAt = frame.Groups[1].Rows[0].Key
	}
	return frame
}

// pursuitRowGroup is one group of the tab's rows, under its key in --json and its title on screen.
type pursuitRowGroup struct {
	key, title string
	rows       []standingRow
}

// pursuitGroups decides which group each row sits in: every pursuit where the focus shown is
// none, else the named shares and then the rest, each in the standing's order and left out
// where empty.
func pursuitGroups(view pursuitsView) []pursuitRowGroup {
	if view.focus == nil {
		return []pursuitRowGroup{{key: pursuitGroupEvery, title: "every pursuit", rows: view.rows}}
	}
	named := pursuitRowGroup{key: pursuitGroupNamed, title: "named shares"}
	rest := pursuitRowGroup{key: pursuitGroupRest}
	restShare := 0.0
	for _, line := range view.rows {
		if line.ShareNamed {
			named.rows = append(named.rows, line)
			continue
		}
		rest.rows = append(rest.rows, line)
		if line.Share != nil {
			restShare += *line.Share
		}
	}
	rest.title = "the rest share " + fraction(restShare)
	return slices.DeleteFunc([]pursuitRowGroup{named, rest}, func(group pursuitRowGroup) bool { return len(group.rows) == 0 })
}

func pursuitKey(number int) string { return "pursuit:" + strconv.Itoa(number) }

func (b *pursuitsBoard) stripLine(now time.Time, view pursuitsView) gotermboard.Line {
	line := gotermboard.Line{gotermboard.C("slate · pursuits", gotermboard.Bold), gotermboard.C("   ", "")}
	switch focus := view.focus; {
	case !view.read && view.viewing == 0:
	case focus == nil && view.viewing == 0:
		line = append(line, gotermboard.C("no focus in force · usage since "+view.window.Since.Local().Format("01-02"), gotermboard.Yellow))
	case focus == nil:
		line = append(line, gotermboard.C(fmt.Sprintf("focus %d · reading…", view.viewing), dimCode))
	default:
		said := fmt.Sprintf("focus %d · ", focus.ID)
		if view.viewing == 0 {
			said += "through " + monthDay(focus.UntilDate)
		} else {
			said += focusSpan(*focus, view.usage)
		}
		line = append(line, gotermboard.C(said, gotermboard.Cyan))
		if view.usage != nil {
			if cut := usageCut(*view.usage); cut != "" {
				line = append(line, gotermboard.C(" · "+cut, gotermboard.Yellow))
			}
		}
		line = append(line, gotermboard.C(" · "+cutText(focus.Statement, statementShown), ""))
	}
	line = append(line, gotermboard.C("   ", ""))
	return append(line, gotermboard.ReadStatus([]gotermboard.Status{b.standing.Status(), b.pursuits.Status(), b.history.Status(), b.usage.Status()}, now)...)
}

// monthDay writes a YYYY-MM-DD day as MM-DD.
func monthDay(day string) string {
	if parsed, err := time.Parse(time.DateOnly, day); err == nil {
		return parsed.Format("01-02")
	}
	return day
}

// focusSpan is an earlier focus's first and last day and how it ended, as 11-01 to 11-20 ·
// replaced. Its last day is its usage read's last, which the API ends where the focus stopped
// holding, so it reads … until that read lands.
func focusSpan(focus api.Focus, usage *api.FocusUsage) string {
	last, ended := "…", "lapsed"
	if usage != nil && len(usage.Days) > 0 {
		last = monthDay(usage.Days[len(usage.Days)-1].Day)
	}
	if focus.EndedTS != nil {
		ended = strValue(focus.EndedReason)
	}
	if focus.Active {
		ended = "in force"
	}
	return fmt.Sprintf("%s to %s · %s", focus.StartedTS.Local().Format("01-02"), last, ended)
}

// cutText is text cut to at most runes, ending in an ellipsis where it was cut.
func cutText(text string, runes int) string {
	if r := []rune(text); len(r) > runes {
		return string(r[:runes-1]) + "…"
	}
	return text
}

// totalGroup is every pursuit's line: the output they drew in the window and the latest work.
// Its panel charts the named shares and the rest by day.
func (b *pursuitsBoard) totalGroup(view pursuitsView, now time.Time) gotermboard.Group {
	output := 0.0
	var latest *time.Time
	for _, line := range view.rows {
		output += line.SplitOutputTokens
		if line.touched != nil && (latest == nil || line.touched.After(*latest)) {
			latest = line.touched
		}
	}
	cells := []gotermboard.Cell{
		gotermboard.C("", ""), gotermboard.C("", ""), gotermboard.C("", ""),
		outputCell(output),
		gotermboard.C("", ""), gotermboard.C("", ""),
		touchedCell(latest, now),
		gotermboard.C("", ""),
	}
	group := gotermboard.Group{
		Key:       "total",
		Title:     gotermboard.Line{gotermboard.C("TOTAL", totalTone)},
		Count:     len(view.rows),
		Counted:   true,
		Cells:     toned(cells, totalTone),
		Underline: gotermboard.RuleThin,
		Heading:   true,
		Gap:       true,
		Panel:     b.totalPanel(view, now),
	}
	switch fault := b.usage.Status().Fault; {
	case view.usage == nil && fault != "":
		group.Below = []gotermboard.Line{{gotermboard.C("usage by day unread: "+fault, gotermboard.Red)}}
	case !view.read:
		group.Below = []gotermboard.Line{dimLine(fmt.Sprintf("reading focus %d…", view.viewing))}
	case len(view.rows) == 0:
		group.Below = []gotermboard.Line{dimLine("No active pursuit, and none drew output in the window.")}
	}
	return group
}

func (b *pursuitsBoard) pursuitGroup(view pursuitsView, key, title string, lines []standingRow, now time.Time) gotermboard.Group {
	rows := make([]gotermboard.Row, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, b.pursuitRow(view, line, now))
	}
	// The title is a Span, so it never widens the column of numbers under it.
	return gotermboard.Group{
		Key:     key,
		Span:    gotermboard.Line{gotermboard.C(title+" · "+strconv.Itoa(len(lines)), tierTone)},
		Heading: true,
		Rows:    rows,
	}
}

func figureCell(f *float64) gotermboard.Cell {
	if f == nil {
		return gotermboard.C(noFigure, dimCode)
	}
	return gotermboard.C(fraction(*f), "")
}

// usageCell is a usage, and a dot for none.
func usageCell(usage float64) gotermboard.Cell {
	if usage <= 0 {
		return gotermboard.C(noFigure, dimCode)
	}
	return gotermboard.C(fraction(usage), "")
}

func outputCell(tokens float64) gotermboard.Cell {
	if tokens <= 0 {
		return gotermboard.C(noFigure, dimCode)
	}
	return gotermboard.C(shortTokenCount(tokens), "")
}

// shortTokenCount writes a count of tokens short, as 19.9k, 1.2M or 1.2B.
func shortTokenCount(tokens float64) string {
	switch {
	case tokens >= 1e9:
		return strconv.FormatFloat(tokens/1e9, 'f', 1, 64) + "B"
	case tokens >= 1e6:
		return strconv.FormatFloat(tokens/1e6, 'f', 1, 64) + "M"
	case tokens >= 1e3:
		return strconv.FormatFloat(tokens/1e3, 'f', 1, 64) + "k"
	}
	return strconv.FormatFloat(math.Round(tokens), 'f', 0, 64)
}

func touchedCell(touched *time.Time, now time.Time) gotermboard.Cell {
	if touched == nil {
		return gotermboard.C(noFigure, dimCode)
	}
	return gotermboard.C(localClock(*touched, now), dimCode)
}

// heldCountCell is a count of what a pursuit holds, and a dot where the pursuits are unread.
func heldCountCell(count int, read bool) gotermboard.Cell {
	if !read {
		return gotermboard.C(noFigure, dimCode)
	}
	return gotermboard.C(strconv.Itoa(count), "")
}

// pursuitRow draws a pursuit on the columns. It names the pursuit, so t from another tab lands
// on it, and / matches its summary.
func (b *pursuitsBoard) pursuitRow(view pursuitsView, line standingRow, now time.Time) gotermboard.Row {
	pursuit, read := view.pursuits[line.Number]
	return gotermboard.Row{
		Key:      pursuitKey(line.Number),
		Entities: []gotermboard.EntityRef{recordEntity(entityKindPursuit, line.Number)},
		Cells: []gotermboard.Cell{
			gotermboard.C(strconv.Itoa(line.Number), gotermboard.Cyan),
			figureCell(line.Share),
			usageCell(line.Usage),
			figureCell(line.Factor),
			outputCell(line.SplitOutputTokens),
			heldCountCell(len(pursuit.Initiatives), read),
			heldCountCell(len(pursuit.Ideas), read),
			touchedCell(line.touched, now),
			gotermboard.C(line.name, ""),
		},
		Panel: b.pursuitPanel(view, line, pursuit, read),
		Text:  pursuit.Summary,
	}
}

// pursuitPanel is a pursuit's figures, its summary, its usage charted by day beside its share,
// and what it holds.
func (b *pursuitsBoard) pursuitPanel(view pursuitsView, line standingRow, pursuit api.Pursuit, read bool) gotermboard.Document {
	return func(width int) []gotermboard.Line {
		lines := []gotermboard.Line{{
			gotermboard.C(line.name, gotermboard.Bold),
			gotermboard.C(fmt.Sprintf(" · share %s · usage %s · factor %s", figureOrDot(line.Share), fraction(line.Usage), figureOrDot(line.Factor)), ""),
		}}
		if read && pursuit.Summary != "" {
			lines = append(lines, plainLine(pursuit.Summary))
		}
		if line.status != "" && line.status != "active" {
			lines = append(lines, gotermboard.Line{gotermboard.C(line.status+", so the focus gives it no share", gotermboard.Magenta)})
		}
		lines = append(lines, gotermboard.Line{}, heading(usageHeading(view)))
		lines = append(lines, b.chartOrWhy(view, func(usage api.FocusUsage) []gotermboard.Line {
			return pursuitUsageChart(usage, view, view.days[line.Number], width)
		})...)
		if read {
			lines = append(lines, gotermboard.Line{}, plainLine(fmt.Sprintf("%d %s · %d %s",
				len(pursuit.Initiatives), plural(len(pursuit.Initiatives), "initiative", "initiatives"),
				len(pursuit.Ideas), plural(len(pursuit.Ideas), "idea", "ideas"))))
			if measure := strValue(pursuit.Measure); measure != "" {
				lines = append(lines, dimLine("measured by "+measure))
			}
		}
		return lines
	}
}

// usageHeading is the chart's heading, naming its focus and any cut to its last days.
func usageHeading(view pursuitsView) string {
	if view.focus == nil {
		if view.usage != nil && len(view.usage.Days) > 0 {
			return fmt.Sprintf("usage by day, the last %d days", len(view.usage.Days))
		}
		return "usage by day"
	}
	heading := fmt.Sprintf("usage by day, focus %d", view.focus.ID)
	if view.usage != nil {
		if cut := usageCut(*view.usage); cut != "" {
			heading += ", " + cut
		}
	}
	return heading
}

// chartOrWhy is the chart drawn from the usage by day of the focus shown, and otherwise why
// there is none. A failed read keeps the last good one, so a chart still drawn says so.
func (b *pursuitsBoard) chartOrWhy(view pursuitsView, draw func(api.FocusUsage) []gotermboard.Line) []gotermboard.Line {
	fault := b.usage.Status().Fault
	switch {
	case view.usage == nil && fault != "":
		return []gotermboard.Line{{gotermboard.C("usage by day unread: "+fault, gotermboard.Red)}}
	case view.usage == nil:
		return []gotermboard.Line{dimLine("reading…")}
	case len(view.usage.Days) == 0:
		return []gotermboard.Line{dimLine("No day to chart.")}
	}
	lines := draw(*view.usage)
	if fault != "" {
		lines = append(lines, dimLine("The last read failed, so this is an earlier one: "+fault))
	}
	return lines
}

// chartSpan is the time axis: from where the read's first day counts to where the focus in force
// stops holding, so the days still to come show empty, or else to the end of the last day read.
func chartSpan(usage api.FocusUsage, view pursuitsView) (time.Time, time.Time) {
	to := usage.Until
	if focus := view.focus; view.viewing == 0 && focus != nil && focus.HeldUntil != nil && focus.HeldUntil.After(to) {
		to = *focus.HeldUntil
	}
	return usage.Since, to
}

func dayLabel(t time.Time) string { return t.Format("01-02") }

// pursuitUsageChart draws a pursuit's usage on each day beside its share, each at the end of its
// day's window. Where a day lacks it, both lines break rather than join across the day.
func pursuitUsageChart(usage api.FocusUsage, view pursuitsView, days []*api.PursuitUsageOnDay, width int) []gotermboard.Line {
	share := make([]*chart.Point, len(usage.Days))
	used := make([]*chart.Point, len(usage.Days))
	for at, figures := range days {
		if figures == nil {
			continue
		}
		when := usage.Days[at].Window.Until
		used[at] = &chart.Point{At: when, Value: figures.Usage}
		if figures.Share != nil {
			share[at] = &chart.Point{At: when, Value: *figures.Share}
		}
	}
	from, to := chartSpan(usage, view)
	shares := dayRuns("share", gotermboard.Yellow, true, share)
	// A share holds until the focus ends, so the last day's runs on over the days still to come.
	if last := share[len(share)-1]; last != nil && to.After(last.At) {
		run := &shares[len(shares)-1]
		run.Points = append(run.Points, chart.Point{At: to, Value: last.Value})
	}
	series := slices.Concat(shares, dayRuns("usage", gotermboard.Cyan, false, used))
	if len(series) == 0 {
		return []gotermboard.Line{dimLine("No day lists it.")}
	}
	drawn := chart.TimeSeries{Series: series, From: from, To: to, TimeLabel: dayLabel, ValueLabel: fraction}
	return append(drawn.Lines(width, usageChartHeight), drawn.Legend())
}

// dayRuns is a line for each run of days holding a reading, all in one code, the first named
// for the legend.
func dayRuns(name, code string, dotted bool, readings []*chart.Point) []chart.Series {
	var series []chart.Series
	var run []chart.Point
	flush := func() {
		if len(run) == 0 {
			return
		}
		named := ""
		if len(series) == 0 {
			named = name
		}
		series = append(series, chart.Series{Name: named, Points: run, Code: code, Dotted: dotted})
		run = nil
	}
	for _, reading := range readings {
		if reading == nil {
			flush()
			continue
		}
		run = append(run, *reading)
	}
	flush()
	return series
}

// totalPanel is the focus shown whole, and every named share and the rest stacked by day.
func (b *pursuitsBoard) totalPanel(view pursuitsView, now time.Time) gotermboard.Document {
	return func(width int) []gotermboard.Line {
		var lines []gotermboard.Line
		switch focus := view.focus; {
		case focus == nil && view.viewing == 0:
			lines = append(lines, heading("No focus is in force"), plainLine("Every pursuit's usage on each day, with no share to stand against."))
		case focus == nil:
			lines = append(lines, heading(fmt.Sprintf("Focus %d", view.viewing)), dimLine("reading…"))
		default:
			said := fmt.Sprintf("Focus %d · set %s by %s · through %s", focus.ID, localClock(focus.StartedTS, now), focus.SetBy, focus.UntilDate)
			lines = append(lines, heading(said), plainLine(focus.Statement))
			if view.viewing != 0 {
				lines = append(lines, gotermboard.Line{gotermboard.C("ended "+focusEnded(*focus, now), gotermboard.Magenta)})
			}
		}
		lines = append(lines, gotermboard.Line{}, heading(usageHeading(view)))
		lines = append(lines, b.chartOrWhy(view, func(usage api.FocusUsage) []gotermboard.Line {
			return totalUsageChart(usage, view, width)
		})...)
		if standing := b.standing.Value(); view.viewing == 0 && view.read {
			unattributed := standing.Unattributed
			lines = append(lines, gotermboard.Line{}, heading("Reaching no pursuit"), plainLine(fmt.Sprintf("%s output in %d %s · %s in no worklog",
				shortTokenCount(float64(unattributed.OutputTokens)), unattributed.WorklogCount, plural(unattributed.WorklogCount, "worklog", "worklogs"),
				shortTokenCount(float64(unattributed.OutsideWorklogsOutputTokens)))))
		}
		return lines
	}
}

// totalUsageChart stacks each named share's usage by day, then the rest's, so the stack is every
// pursuit's work on a day. With no focus naming any, the most used on the last day stand apart.
func totalUsageChart(usage api.FocusUsage, view pursuitsView, width int) []gotermboard.Line {
	var apart []standingRow
	for _, group := range pursuitGroups(view) {
		switch group.key {
		case pursuitGroupNamed:
			apart = group.rows
		case pursuitGroupEvery:
			apart = slices.Clone(group.rows)
			slices.SortStableFunc(apart, func(a, b standingRow) int { return cmp.Compare(b.Usage, a.Usage) })
			apart = slices.DeleteFunc(apart[:min(len(apart), totalChartMost)], func(line standingRow) bool { return line.Usage <= 0 })
		}
	}
	isApart := map[int]bool{}
	var layers []chart.Layer
	for at, line := range apart {
		isApart[line.Number] = true
		name := line.name
		if line.Share != nil {
			name += " " + fraction(*line.Share)
		}
		layers = append(layers, chart.Layer{Name: name, Code: totalLayerCodes[at%len(totalLayerCodes)], Points: dayUsage(usage, func(n int) bool { return n == line.Number })})
	}
	rest := chart.Layer{Name: "the rest", Code: restLayerCode, Points: dayUsage(usage, func(n int) bool { return !isApart[n] })}
	if len(apart) == 0 {
		rest.Name = "every pursuit"
	}
	layers = append(layers, rest)
	from, to := chartSpan(usage, view)
	stack := chart.StackedArea{Layers: layers, From: from, To: to, Max: 1, TimeLabel: dayLabel, ValueLabel: fraction}
	return append(stack.Lines(width, usageChartHeight), stack.Legend())
}

// dayUsage sums, on each day, the usage of each pursuit that holds accepts, placed at the end of
// the day's window. Where a day lacks a pursuit, it drew nothing in that window.
func dayUsage(usage api.FocusUsage, holds func(number int) bool) []chart.Point {
	points := make([]chart.Point, 0, len(usage.Days))
	for _, day := range usage.Days {
		sum := 0.0
		for _, figures := range day.Pursuits {
			if holds(figures.Number) {
				sum += figures.Usage
			}
		}
		points = append(points, chart.Point{At: day.Window.Until, Value: sum})
	}
	return points
}

// pursuitsSnapshot is what --json prints, as tui-pursuits.schema.json describes it.
type pursuitsSnapshot struct {
	SchemaVersion int                    `json:"schema_version"`
	TakenAt       time.Time              `json:"taken_at"`
	Tab           string                 `json:"tab"`
	Focus         *api.Focus             `json:"focus"`
	Window        *api.FocusWindow       `json:"window"`
	Days          *pursuitsSnapshotDays  `json:"days"`
	Rows          []pursuitSnapshotRow   `json:"rows"`
	Unattributed  *api.UnattributedUsage `json:"unattributed"`
	Reads         []gotermboard.Status   `json:"reads"`
}

// pursuitsSnapshotDays is the days the usage by day answers, and whether a focus longer than the
// read answers was cut to its last days.
type pursuitsSnapshotDays struct {
	First string    `json:"first"`
	Last  string    `json:"last"`
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
	Cut   bool      `json:"cut"`
}

// pursuitSnapshotRow is one pursuit's row, under the names `slate focus show --json` gives its
// figures, with the group it sits in, what GET /pursuits says of it, and each day of the usage
// read that lists it, under the names `slate focus usage --json` gives them.
type pursuitSnapshotRow struct {
	api.PursuitFigures
	Name            string               `json:"name"`
	Status          string               `json:"status"`
	TouchedTS       *time.Time           `json:"touched_ts"`
	Group           string               `json:"group"`
	Summary         *string              `json:"summary"`
	InitiativeCount *int                 `json:"initiative_count"`
	IdeaCount       *int                 `json:"idea_count"`
	ByDay           []pursuitSnapshotDay `json:"by_day"`
}

type pursuitSnapshotDay struct {
	Day string `json:"day"`
	api.PursuitUsageOnDay
}

// Snapshot is what --json prints of the tab.
func (b *pursuitsBoard) Snapshot(now time.Time) pursuitsSnapshot {
	view := b.view()
	snapshot := pursuitsSnapshot{
		SchemaVersion: pursuitsSchemaVersion, TakenAt: now, Tab: tabPursuits, Focus: view.focus,
		Rows:  []pursuitSnapshotRow{},
		Reads: []gotermboard.Status{b.standing.Status(), b.pursuits.Status(), b.history.Status(), b.usage.Status()},
	}
	if view.read {
		snapshot.Window = &view.window
	}
	if usage := view.usage; usage != nil && len(usage.Days) > 0 {
		snapshot.Days = &pursuitsSnapshotDays{
			First: usage.Days[0].Day, Last: usage.Days[len(usage.Days)-1].Day, Since: usage.Since, Until: usage.Until, Cut: usageCut(*usage) != "",
		}
	}
	if view.viewing == 0 && view.read {
		unattributed := b.standing.Value().Unattributed
		snapshot.Unattributed = &unattributed
	}
	groupOf := map[int]string{}
	for _, group := range pursuitGroups(view) {
		for _, line := range group.rows {
			groupOf[line.Number] = group.key
		}
	}
	for _, line := range view.rows {
		row := pursuitSnapshotRow{
			PursuitFigures: line.PursuitFigures, Name: line.name, Status: line.status, TouchedTS: line.touched,
			Group: groupOf[line.Number], ByDay: []pursuitSnapshotDay{},
		}
		if pursuit, read := view.pursuits[line.Number]; read {
			initiatives, ideas := len(pursuit.Initiatives), len(pursuit.Ideas)
			row.Summary, row.InitiativeCount, row.IdeaCount = &pursuit.Summary, &initiatives, &ideas
		}
		for at, figures := range view.days[line.Number] {
			if figures != nil {
				row.ByDay = append(row.ByDay, pursuitSnapshotDay{Day: view.usage.Days[at].Day, PursuitUsageOnDay: *figures})
			}
		}
		snapshot.Rows = append(snapshot.Rows, row)
	}
	return snapshot
}
