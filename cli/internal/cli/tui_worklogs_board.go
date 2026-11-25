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
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/gotermboard"

	"github.com/datapointchris/slate/cli/internal/api"
)

const tabWorklogs = "worklogs"

// worklogsSchemaVersion versions what --json prints, whose shape tui-worklogs.schema.json describes.
const worklogsSchemaVersion = 1

// Every read names a limit, because every worklog of every state is megabytes. The live and
// expired reads ask for the most the tab reads. The ended read asks for the newest step of them,
// which + and - move a step at a time up to that most.
const (
	worklogsReadMost  = 300
	endedWorklogsStep = 100
)

// waitsOnShown caps the WAITS ON column, so a long wait leaves the record's title room.
const waitsOnShown = 34

var worklogColumns = []gotermboard.Column{
	{Title: "WORKLOG"},
	{Title: "STEP"},
	{Title: "STAGE"},
	{Title: "SINCE"},
	{Title: "SESS", Right: true},
	{Title: "HOLDER"},
	{Title: "WAITS ON", Max: waitsOnShown},
	{Title: "RECORD"},
}

// The keys of the tab's lines. Each line under a worklog carries the worklog's name second, so
// no two worklogs' lines share a key.
const (
	worklogKeyPrefix        = "worklog:"
	worklogSessionKeyPrefix = "worklog-session:"
	worklogLinkKeyPrefix    = "worklog-link:"
	worklogEmptyKeyPrefix   = "worklog-empty:"
)

func worklogKey(name string) string { return worklogKeyPrefix + name }

func worklogStateKey(state string) string { return "worklogs:" + state }

func worklogSessionKey(name, session string) string {
	return worklogSessionKeyPrefix + name + ":" + session
}

func worklogLinkKey(name string, link api.WorklogLink) string {
	return worklogLinkKeyPrefix + name + ":" + link.Kind + ":" + link.Ref
}

// linkOfKey is the kind and ref a link row's key holds. A ref may hold colons, so it is the rest.
func linkOfKey(key string) (kind, ref string, ok bool) {
	rest, found := strings.CutPrefix(key, worklogLinkKeyPrefix)
	if !found {
		return "", "", false
	}
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) != 3 {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// cursorWorklog is the worklog holding the cursor's line, and its record.
type cursorWorklog struct {
	name   string
	record int
}

// endedRead is one read of the newest ended worklogs, with the limit it asked for.
type endedRead struct {
	worklogs []api.Worklog
	limit    int
}

// recordTotals is each record's totals as last read, by number.
type recordTotals map[int]api.WorkTotals

// totalsFault is the last totals read that failed, on which record, and why.
type totalsFault struct {
	record int
	fault  string
}

// worklogsBoard is the worklogs tab. Its reads run off the program's goroutine and touch only
// their sources, the atomics and their own failure. The keys and Frame run on it.
type worklogsBoard struct {
	client        *api.Client
	live, expired *gotermboard.Source[[]api.Worklog]
	ended         *gotermboard.Source[endedRead]
	// found is a jump's sought worklog, where none of the bounded reads holds it.
	found           *gotermboard.Source[[]api.Worklog]
	totals          *gotermboard.Source[recordTotals]
	endedLimit      atomic.Int64
	sought          atomic.Pointer[string]
	cursor          atomic.Pointer[cursorWorklog]
	lastTotalsFault atomic.Pointer[totalsFault]
	// lines is each line's worklog as the last Frame drew it, which CursorOn looks the cursor up in.
	lines          map[string]cursorWorklog
	liveFailure    error
	expiredFailure error
	endedFailure   error
	totalsFailure  error
}

func newWorklogsBoard(client *api.Client) *worklogsBoard {
	b := &worklogsBoard{client: client}
	b.endedLimit.Store(endedWorklogsStep)
	b.live = gotermboard.NewSource("live worklogs", boardReadEvery, func(ctx context.Context) ([]api.Worklog, string, error) {
		worklogs, err := b.list(ctx, api.WorklogStateLive, worklogsReadMost)
		b.liveFailure = err
		return worklogs, "", err
	})
	b.expired = gotermboard.NewSource("expired worklogs", boardReadEvery, func(ctx context.Context) ([]api.Worklog, string, error) {
		worklogs, err := b.list(ctx, api.WorklogStateExpired, worklogsReadMost)
		b.expiredFailure = err
		return worklogs, "", err
	})
	b.ended = gotermboard.NewSource("ended worklogs", boardTextReadEvery, func(ctx context.Context) (endedRead, string, error) {
		limit := int(b.endedLimit.Load())
		worklogs, err := b.list(ctx, api.WorklogStateEnded, limit)
		b.endedFailure = err
		return endedRead{worklogs: worklogs, limit: limit}, "", err
	})
	b.found = gotermboard.NewSource("sought worklog", boardTextReadEvery, b.readSought)
	b.totals = gotermboard.NewSource("cursor's record totals", boardTextReadEvery, b.readCursorTotals)
	return b
}

// readSought reads a jump's sought worklog by its name, where none of the bounded reads
// holds it, so a jump to one that ended long ago lands on its row.
func (b *worklogsBoard) readSought(ctx context.Context) ([]api.Worklog, string, error) {
	name := b.sought.Load()
	if name == nil || b.listsHold(*name) {
		return nil, "", nil
	}
	worklog, err := b.client.GetWorklog(ctx, *name)
	var refused *api.APIError
	if errors.As(err, &refused) && refused.NotFound() {
		return nil, "", nil
	}
	if err != nil {
		return b.found.Value(), "", err
	}
	return []api.Worklog{worklog}, "", nil
}

func (b *worklogsBoard) listsHold(name string) bool {
	named := func(worklog api.Worklog) bool { return worklog.Name == name }
	return slices.ContainsFunc(b.live.Value(), named) || slices.ContainsFunc(b.expired.Value(), named) ||
		slices.ContainsFunc(b.ended.Value().worklogs, named)
}

func (b *worklogsBoard) list(ctx context.Context, state string, limit int) ([]api.Worklog, error) {
	return b.client.ListWorklogs(ctx, api.WorklogFilter{State: state}, &limit)
}

// readCursorTotals reads the totals of the record holding the cursor's worklog, so its panel names
// the record's spend and estimate. It reads again each tick while the cursor stays there.
func (b *worklogsBoard) readCursorTotals(ctx context.Context) (recordTotals, string, error) {
	held := b.totals.Value()
	cursor := b.cursor.Load()
	if cursor == nil {
		return held, "", nil
	}
	record := cursor.record
	totals, err := b.client.GetTotals(ctx, api.TotalsFilter{Record: strconv.Itoa(record)}, api.Window{})
	b.totalsFailure = err
	if err != nil {
		b.lastTotalsFault.Store(&totalsFault{record: record, fault: err.Error()})
		return held, "", err
	}
	b.lastTotalsFault.Store(nil)
	next := make(recordTotals, len(held)+1)
	maps.Copy(next, held)
	next[record] = totals
	return next, "", nil
}

// Sources is gotermboard.Board's.
func (b *worklogsBoard) Sources() []gotermboard.Refresher {
	return []gotermboard.Refresher{b.live, b.expired, b.ended, b.found, b.totals}
}

// Groupings is gotermboard.Board's.
func (b *worklogsBoard) Groupings() []string { return nil }

// CursorOn is gotermboard.CursorBoard's. A record's totals read on the next tick where none is
// held, so holding a key down reads only the record it stops on.
func (b *worklogsBoard) CursorOn(key string) {
	at, ok := b.lines[key]
	if !ok {
		b.cursor.Store(nil)
		return
	}
	b.cursor.Store(&at)
	if _, held := b.totals.Value()[at.record]; !held {
		b.totals.Force()
	}
}

// EntityKinds is gotermboard.EntityBoard's.
func (b *worklogsBoard) EntityKinds() []string { return []string{entityKindWorklog} }

// ShowEntity is gotermboard.EntityBoard's. A worklog none of the bounded reads holds is read by
// its name, and drawn under its state until a later jump seeks another.
func (b *worklogsBoard) ShowEntity(ref gotermboard.EntityRef) bool {
	if ref.Kind == entityKindWorklog {
		b.sought.Store(&ref.ID)
		b.found.Force()
	}
	return false
}

// readOnce is every read a printed frame draws from, the opening worklog's record totals last,
// because the lists say which worklog that is.
func (b *worklogsBoard) readOnce(ctx context.Context) error {
	gotermboard.RefreshAll(ctx, b.live, b.expired, b.ended)
	if err := errors.Join(b.liveFailure, b.expiredFailure, b.endedFailure); err != nil {
		return err
	}
	if live := b.groups()[0].worklogs; len(live) > 0 {
		b.cursor.Store(&cursorWorklog{name: live[0].Name, record: live[0].RecordNumber})
		b.totals.Refresh(ctx)
	}
	return b.totalsFailure
}

// worklogGroup is one read's worklogs, under the state that read asked for.
type worklogGroup struct {
	state    string
	worklogs []api.Worklog
	limit    int
	// cut says the read answered as many as it asked for, so more may lie past it.
	cut   bool
	read  bool
	fault string
}

// groups is the live, the expired and the ended worklogs. A worklog that changed state between
// two reads sits once, under the read that landed last.
func (b *worklogsBoard) groups() []worklogGroup {
	ended := b.ended.Value()
	groups := []worklogGroup{
		listedGroup(api.WorklogStateLive, b.live.Value(), worklogsReadMost, b.live.Status()),
		listedGroup(api.WorklogStateExpired, b.expired.Value(), worklogsReadMost, b.expired.Status()),
		listedGroup(api.WorklogStateEnded, ended.worklogs, cmp.Or(ended.limit, int(b.endedLimit.Load())), b.ended.Status()),
	}
	readAt := []time.Time{b.live.Status().ReadAt, b.expired.Status().ReadAt, b.ended.Status().ReadAt}
	newestFirst := []int{0, 1, 2}
	slices.SortStableFunc(newestFirst, func(i, j int) int { return readAt[j].Compare(readAt[i]) })
	placedIn := map[string]int{}
	for _, at := range newestFirst {
		for _, worklog := range groups[at].worklogs {
			if _, placed := placedIn[worklog.Name]; !placed {
				placedIn[worklog.Name] = at
			}
		}
	}
	for at := range groups {
		groups[at].worklogs = slices.DeleteFunc(slices.Clone(groups[at].worklogs), func(worklog api.Worklog) bool { return placedIn[worklog.Name] != at })
	}
	for _, worklog := range b.found.Value() {
		at := slices.IndexFunc(groups, func(group worklogGroup) bool { return group.state == worklog.State })
		if _, placed := placedIn[worklog.Name]; !placed && at >= 0 {
			groups[at].worklogs = append(groups[at].worklogs, worklog)
		}
	}
	return groups
}

func listedGroup(state string, worklogs []api.Worklog, limit int, status gotermboard.Status) worklogGroup {
	return worklogGroup{
		state: state, worklogs: worklogs, limit: limit, cut: len(worklogs) >= limit,
		read: !status.ReadAt.IsZero(), fault: status.Fault,
	}
}

// openingWorklog is the key of the first live worklog, which the cursor starts on.
func openingWorklog(groups []worklogGroup) string {
	if live := groups[0].worklogs; len(live) > 0 {
		return worklogKey(live[0].Name)
	}
	return ""
}

// Actions is gotermboard.Keyed's.
func (b *worklogsBoard) Actions() []gotermboard.Action {
	bound := func(steps int) func(*gotermboard.Model) tea.Cmd {
		return func(m *gotermboard.Model) tea.Cmd {
			limit := min(max(int(b.endedLimit.Load())+steps*endedWorklogsStep, endedWorklogsStep), worklogsReadMost)
			b.endedLimit.Store(int64(limit))
			b.ended.Force()
			m.Say(fmt.Sprintf("reading the newest %d ended worklogs", limit))
			return nil
		}
	}
	return []gotermboard.Action{
		{
			Keys: []string{"enter"}, Label: "unfold a worklog into its sessions and links, and open a pull request in prs", Footer: "enter open",
			Run: func(m *gotermboard.Model) tea.Cmd {
				if kind, ref, ok := linkOfKey(m.Cursor()); ok && kind == api.WorklogLinkPullRequest {
					return gotermboard.GoTo(prsOnPullRequest.Name, gotermboard.EntityRef{Kind: entityKindPullRequest, ID: ref})
				}
				return m.Toggle()
			},
		},
		{
			Keys: []string{"o"}, Label: "open the link in the browser, where it is a web link", Footer: "o browser",
			Run: func(m *gotermboard.Model) tea.Cmd {
				if _, ref, ok := linkOfKey(m.Cursor()); ok {
					return openWebLink(m, ref)
				}
				return nil
			},
			Applies: func(m *gotermboard.Model) bool {
				_, _, ok := linkOfKey(m.Cursor())
				return ok
			},
		},
		{
			Keys: []string{"+", "="}, Label: fmt.Sprintf("read %d more of the newest ended worklogs", endedWorklogsStep), Footer: "+/- ended",
			Run: bound(1), Applies: func(*gotermboard.Model) bool { return b.moreEnded() },
		},
		{
			Keys: []string{"-"}, Label: fmt.Sprintf("read %d fewer of the newest ended worklogs", endedWorklogsStep),
			Run: bound(-1), Applies: func(*gotermboard.Model) bool { return b.endedLimit.Load() > endedWorklogsStep },
		},
	}
}

// Receive is gotermboard.ReceivingBoard's.
func (b *worklogsBoard) Receive(m *gotermboard.Model, msg tea.Msg) tea.Cmd {
	sayLinkOpened(m, msg)
	return nil
}

// moreEnded says the ended read answered all it asked for, and the bound can grow.
func (b *worklogsBoard) moreEnded() bool {
	read := b.ended.Value()
	return b.endedLimit.Load() < worklogsReadMost && read.limit > 0 && len(read.worklogs) >= read.limit
}

// Frame is gotermboard.Board's.
func (b *worklogsBoard) Frame(now time.Time, _ string) gotermboard.Frame {
	groups := b.groups()
	frame := gotermboard.Frame{Columns: worklogColumns, Strip: []gotermboard.Line{b.stripLine(now, groups), {}}}
	lines := map[string]cursorWorklog{}
	for _, group := range groups {
		frame.Groups = append(frame.Groups, b.stateGroup(group, now, lines))
	}
	b.lines = lines
	frame.StartAt = openingWorklog(groups)
	return frame
}

func (b *worklogsBoard) stripLine(now time.Time, groups []worklogGroup) gotermboard.Line {
	line := gotermboard.Line{gotermboard.C("slate · worklogs", gotermboard.Bold), gotermboard.C("   ", "")}
	var counts []string
	for _, group := range groups {
		if !group.read {
			continue
		}
		if group.cut {
			counts = append(counts, fmt.Sprintf("newest %d of the %s", group.limit, group.state))
		} else {
			counts = append(counts, fmt.Sprintf("%d %s", len(group.worklogs), group.state))
		}
	}
	if len(counts) > 0 {
		line = append(line, gotermboard.C(strings.Join(counts, " · "), ""), gotermboard.C("   ", ""))
	}
	// The totals and the sought worklog tick without reading while there is nothing to read, so
	// only their faults reach the strip, and the age is the lists'.
	totals, found := b.totals.Status(), b.found.Status()
	totals.ReadAt, found.ReadAt = time.Time{}, time.Time{}
	return append(line, gotermboard.ReadStatus([]gotermboard.Status{b.live.Status(), b.expired.Status(), b.ended.Status(), found, totals}, now)...)
}

// stateGroup is one state's worklogs, the live open and the rest folded. Its title is a Span,
// so its count never widens the column of worklog names under it. Each line under a worklog
// goes into lines with that worklog.
func (b *worklogsBoard) stateGroup(group worklogGroup, now time.Time, lines map[string]cursorWorklog) gotermboard.Group {
	span := gotermboard.Line{gotermboard.C(group.state, tierTone)}
	switch {
	case group.cut:
		span = append(span, gotermboard.C(fmt.Sprintf(" · newest %d", group.limit), tierTone))
		if group.state == api.WorklogStateEnded {
			span = append(span, gotermboard.C("  "+endedBoundNote(group.limit), dimCode))
		}
	case group.read:
		span = append(span, gotermboard.C(" · "+strconv.Itoa(len(group.worklogs)), tierTone))
	}
	g := gotermboard.Group{
		Key:     worklogStateKey(group.state),
		Span:    span,
		Heading: true,
		Folded:  group.state != api.WorklogStateLive,
	}
	switch {
	case !group.read && group.fault != "":
		g.Below = []gotermboard.Line{{gotermboard.C(fmt.Sprintf("The %s worklogs are unread: %s", group.state, group.fault), gotermboard.Red)}}
	case !group.read:
		g.Below = []gotermboard.Line{dimLine("reading…")}
	}
	for _, worklog := range group.worklogs {
		drawn := b.worklogGroup(worklog, now)
		at := cursorWorklog{name: worklog.Name, record: worklog.RecordNumber}
		lines[drawn.Key] = at
		for _, row := range drawn.Rows {
			lines[row.Key] = at
		}
		g.Groups = append(g.Groups, drawn)
	}
	return g
}

// endedBoundNote says what reads past the ended bound.
func endedBoundNote(limit int) string {
	if limit >= worklogsReadMost {
		return fmt.Sprintf("%d is the most the tab reads", worklogsReadMost)
	}
	return fmt.Sprintf("+ reads %d more", endedWorklogsStep)
}

// worklogRefs is what every line under a worklog names: the worklog and its record.
func worklogRefs(worklog api.Worklog) []gotermboard.EntityRef {
	return []gotermboard.EntityRef{{Kind: entityKindWorklog, ID: worklog.Name}, recordEntity(worklog.Record.Kind, worklog.RecordNumber)}
}

// worklogEntities is what a worklog's line names: the worklog, its record, and each pull request
// it links, so t goes to the record's tab or to prs.
func worklogEntities(worklog api.Worklog) []gotermboard.EntityRef {
	refs := worklogRefs(worklog)
	for _, link := range worklog.Links {
		if link.Kind == api.WorklogLinkPullRequest {
			refs = append(refs, gotermboard.EntityRef{Kind: entityKindPullRequest, ID: link.Ref})
		}
	}
	return refs
}

// worklogGroup is a worklog on the columns, which enter unfolds into its sessions and links.
func (b *worklogsBoard) worklogGroup(worklog api.Worklog, now time.Time) gotermboard.Group {
	sessions := sessionsIn(worklog)
	waiting := ""
	if claim := worklog.Claim; claim != nil && claim.Waiting != nil {
		waiting = claim.Waiting.WaitingOn
	}
	step := gotermboard.C(orDash(strValue(worklog.Step)), "")
	if worklog.Step == nil {
		step.Code = dimCode
	}
	text := []string{filterNumber(worklog.RecordNumber), worklog.OpenedBy}
	if claim := worklog.Claim; claim != nil {
		text = append(text, claim.Holder)
	}
	for _, session := range sessions {
		text = append(text, session.id)
	}
	return gotermboard.Group{
		Key:      worklogKey(worklog.Name),
		Entities: worklogEntities(worklog),
		Title:    gotermboard.Line{gotermboard.C(worklog.Name, gotermboard.Cyan)},
		Cells: []gotermboard.Cell{
			step,
			gotermboard.C(worklog.Stage, ""),
			sinceCell(worklog, now),
			gotermboard.C(strconv.Itoa(len(sessions)), ""),
			gotermboard.C(worklogHolder(worklog), ""),
			gotermboard.C(waiting, gotermboard.Yellow),
			gotermboard.C(worklog.Record.Title, ""),
		},
		Folded: true,
		Rows:   worklogRows(worklog, sessions, now),
		Text:   strings.Join(text, " "),
		Panel:  func(int) []gotermboard.Line { return b.worklogPanel(worklog, sessions, now) },
	}
}

// sinceCell is when the worklog came to its state: its start while live, and where it stopped
// being under way once it ran out or ended.
func sinceCell(worklog api.Worklog, now time.Time) gotermboard.Cell {
	since := worklog.HeldUntil
	if worklog.State == api.WorklogStateLive {
		since = &worklog.StartedTS
	}
	if since == nil {
		return gotermboard.C("—", dimCode)
	}
	return gotermboard.C(localClock(*since, now), dimCode)
}

// worklogHolder is who holds the worklog's claim, as a person tells holders apart: its session's
// first group, or the holder's name where no session holds it.
func worklogHolder(worklog api.Worklog) string {
	claim := worklog.Claim
	switch {
	case claim == nil:
		return "—"
	case strValue(claim.Session) != "":
		return shortSession(*claim.Session)
	}
	return claim.Holder
}

// linkKindWidth is the widest word an unfolded row leads with.
const linkKindWidth = len("pull request")

// linkKindWord is a link's kind as a row says it.
func linkKindWord(kind string) string { return strings.ReplaceAll(kind, "_", " ") }

// linkName is a link's ref as a person reads it: a commit's short hash, a pull request as
// owner/name#number, and any other ref whole.
func linkName(link api.WorklogLink) string {
	switch {
	case link.Kind == api.WorklogLinkPullRequest:
		return pullRequestName(link.Ref)
	case link.Kind == api.WorklogLinkCommit && !webLink(link.Ref) && len(link.Ref) > shortHashLength:
		return link.Ref[:shortHashLength]
	}
	return link.Ref
}

// shortHashLength is how much of a commit's hash a row shows, as git's short form does.
const shortHashLength = 7

// worklogRows is a row for each session that joined the worklog, then for each thing it links.
func worklogRows(worklog api.Worklog, sessions []workedSession, now time.Time) []gotermboard.Row {
	refs := worklogRefs(worklog)
	var rows []gotermboard.Row
	for _, session := range sessions {
		rows = append(rows, worklogSessionRow(worklog, session, refs, now))
	}
	for _, link := range worklog.Links {
		rows = append(rows, worklogLinkRow(worklog, link, refs, now))
	}
	if len(rows) == 0 {
		rows = []gotermboard.Row{{
			Key: worklogEmptyKeyPrefix + worklog.Name, Entities: refs,
			Span: gotermboard.Line{gotermboard.C("No session joined it, and it links nothing.", dimCode)},
		}}
	}
	return rows
}

// spanClock is when something started and ended, or that it still runs.
func spanClock(started time.Time, ended *time.Time, now time.Time) string {
	if ended == nil {
		return localClock(started, now) + " → now"
	}
	return localClock(started, now) + " → " + localClock(*ended, now)
}

func worklogSessionRow(worklog api.Worklog, session workedSession, refs []gotermboard.EntityRef, now time.Time) gotermboard.Row {
	lines := []gotermboard.Line{
		plainLine("session " + session.id),
		plainLine(fmt.Sprintf("in %s, %s", worklog.Name, spanClock(session.started, session.ended, now))),
	}
	if session.directory != "" {
		lines = append(lines, dimLine(session.directory))
	}
	lines = append(lines, plainLine(shortTokensOrNone(session.tokens)))
	return gotermboard.Row{
		Key:      worklogSessionKey(worklog.Name, session.id),
		Entities: refs,
		Span: gotermboard.Line{
			gotermboard.C(padRight("session", linkKindWidth)+" ", ""),
			gotermboard.C(shortSession(session.id), ""),
			gotermboard.C("  "+spanClock(session.started, session.ended, now), dimCode),
		},
		Text:     session.id + " " + session.directory,
		Document: func(int) []gotermboard.Line { return lines },
	}
}

func worklogLinkRow(worklog api.Worklog, link api.WorklogLink, refs []gotermboard.EntityRef, now time.Time) gotermboard.Row {
	code, opens := "", "o names it, since it is no web link"
	switch {
	case link.Kind == api.WorklogLinkPullRequest:
		code, opens = gotermboard.Magenta, pullRequestOpens
		refs = append(slices.Clone(refs), gotermboard.EntityRef{Kind: entityKindPullRequest, ID: link.Ref})
	case webLink(link.Ref):
		opens = "o opens it in the browser"
	}
	lines := []gotermboard.Line{
		heading(linkKindWord(link.Kind) + " " + linkName(link)),
		plainLine(link.Ref),
		{},
		plainLine(fmt.Sprintf("in %s, linked %s by %s", orDash(strValue(link.Repo)), localClock(link.LinkedTS, now), link.LinkedBy)),
	}
	if session := strValue(link.Session); session != "" {
		lines = append(lines, dimLine("from session "+session))
	}
	lines = append(lines, gotermboard.Line{}, dimLine(opens))
	return gotermboard.Row{
		Key:      worklogLinkKey(worklog.Name, link),
		Entities: refs,
		Span: gotermboard.Line{
			gotermboard.C(padRight(linkKindWord(link.Kind), linkKindWidth)+" ", code),
			gotermboard.C(orDash(strValue(link.Repo))+"  ", ""),
			gotermboard.C(linkName(link), gotermboard.Cyan),
			gotermboard.C("  "+localClock(link.LinkedTS, now), dimCode),
		},
		Text:     link.Ref,
		Document: func(int) []gotermboard.Line { return lines },
	}
}

// worklogPanel is the worklog whole: its record, its claim and every wait, each session's span,
// its links, the document versions it wrote, then its tokens beside its record's.
func (b *worklogsBoard) worklogPanel(worklog api.Worklog, sessions []workedSession, now time.Time) []gotermboard.Line {
	facts := []string{worklog.Name, orDash(strValue(worklog.Step)), worklog.Stage, "started " + localClock(worklog.StartedTS, now)}
	if worklog.EndedTS != nil {
		ended := "ended " + localClock(*worklog.EndedTS, now)
		if outcome := strValue(worklog.Outcome); outcome != "" {
			ended += " as " + outcome
		}
		facts = append(facts, ended)
	}
	lines := []gotermboard.Line{
		heading(strings.Join(facts, " · ")),
		plainLine(fmt.Sprintf("%s %d · %s", worklog.Record.Kind, worklog.RecordNumber, worklog.Record.Title)),
	}
	lines = append(lines, claimLines(worklog, now)...)
	if len(sessions) > 0 {
		lines = append(lines, gotermboard.Line{}, heading("sessions"))
		for _, session := range sessions {
			lines = append(lines, gotermboard.Line{
				gotermboard.C("  "+shortSession(session.id), ""),
				gotermboard.C("   "+spanClock(session.started, session.ended, now), dimCode),
			})
		}
	}
	if len(worklog.Links) > 0 {
		lines = append(lines, gotermboard.Line{}, heading("links"))
		for _, link := range worklog.Links {
			lines = append(lines, gotermboard.Line{
				gotermboard.C("  "+padRight(linkKindWord(link.Kind), linkKindWidth)+"  ", ""),
				gotermboard.C(orDash(strValue(link.Repo))+"  ", ""),
				gotermboard.C(linkName(link), gotermboard.Cyan),
				gotermboard.C("  "+localClock(link.LinkedTS, now), dimCode),
			})
		}
	}
	if len(worklog.DocumentVersions) > 0 {
		wrote := make([]string, 0, len(worklog.DocumentVersions))
		for _, version := range worklog.DocumentVersions {
			wrote = append(wrote, fmt.Sprintf("%s v%d", version.Address, version.Version))
		}
		lines = append(lines, gotermboard.Line{}, gotermboard.Line{gotermboard.C("wrote  ", gotermboard.Bold), gotermboard.C(strings.Join(wrote, " · "), "")})
	}
	return append(lines, b.tokenLines(worklog)...)
}

// claimLines is the claim that opened the worklog, and every stretch its holder spent waiting,
// the wait still open in yellow.
func claimLines(worklog api.Worklog, now time.Time) []gotermboard.Line {
	claim := worklog.Claim
	if claim == nil {
		return []gotermboard.Line{{}, dimLine("opened by " + worklog.OpenedBy + " with no claim")}
	}
	held := "holds until " + localClock(claim.ExpiresTS, now)
	switch {
	case claim.EndedTS != nil:
		held = fmt.Sprintf("ended %s as %s", localClock(*claim.EndedTS, now), orDash(strValue(claim.Ending)))
	case claim.State == api.ClaimStateExpired:
		held = "ran out " + localClock(claim.ExpiresTS, now)
	}
	lines := []gotermboard.Line{{}, heading("claim"), plainLine(claim.Holder), plainLine(held)}
	for _, wait := range worklog.Waits {
		code := dimCode
		if wait.EndedTS == nil {
			code = gotermboard.Yellow
		}
		lines = append(lines, gotermboard.Line{
			gotermboard.C("waits on "+wait.WaitingOn, code),
			gotermboard.C("   "+spanClock(wait.StartedTS, wait.EndedTS, now), dimCode),
		})
	}
	return lines
}

// tokenRow is one line of the panel's table of tokens: a count per kind, or what it says in
// their place.
type tokenRow struct {
	label string
	kinds []string
	said  gotermboard.Cell
}

func shortCount(count int) string { return shortTokenCount(float64(count)) }

func spentRow(label string, tokens *api.Tokens) tokenRow {
	if tokens == nil {
		return tokenRow{label: label, said: gotermboard.C("none reported", dimCode)}
	}
	return tokenRow{label: label, kinds: mapped(tokens.ByKind(), shortCount)}
}

// estimatedRow is an estimate's counts, with a dash for a kind not estimated.
func estimatedRow(label string, estimate api.TokenEstimate) tokenRow {
	return tokenRow{label: label, kinds: mapped(estimate.ByKind(), func(count *int) string {
		if count == nil {
			return "—"
		}
		return shortCount(*count)
	})}
}

// tokenLines is a table of tokens by kind: the worklog's own, its own estimate where it holds
// one, and its record's total and estimate as GET /totals answers them, then the preflight
// worklog that set that estimate.
func (b *worklogsBoard) tokenLines(worklog api.Worklog) []gotermboard.Line {
	number := strconv.Itoa(worklog.RecordNumber)
	rows := []tokenRow{spentRow("this worklog", worklog.Tokens)}
	if worklog.Estimate != nil {
		rows = append(rows, estimatedRow("its estimate", *worklog.Estimate))
	}
	totals, read := b.totals.Value()[worklog.RecordNumber]
	fault := b.lastTotalsFault.Load()
	if fault != nil && fault.record != worklog.RecordNumber {
		fault = nil
	}
	var notes []gotermboard.Line
	switch {
	case read:
		rows = append(rows, spentRow(number+" all its work", totals.Tokens))
		if preflight := totals.Estimate; preflight != nil {
			rows = append(rows, estimatedRow(number+" estimate", preflight.Tokens))
			notes = append(notes, dimLine(fmt.Sprintf("%s's estimate is the one preflight %s set", number, preflight.Worklog)))
		} else {
			rows = append(rows, tokenRow{label: number + " estimate", said: gotermboard.C("none until a preflight sets one", dimCode)})
		}
		if fault != nil {
			notes = append(notes, dimLine("The last totals read failed, so these are earlier: "+fault.fault))
		}
	case fault != nil:
		rows = append(rows, tokenRow{label: number + " all its work", said: gotermboard.C("unread: "+fault.fault, gotermboard.Red)})
	default:
		rows = append(rows, tokenRow{label: number + " all its work", said: gotermboard.C("reading…", dimCode)})
	}
	// Each kind's title takes two lines, its key's first word over the rest, so the table fits a
	// panel a third of a 200-column screen wide.
	firsts, rests := make([]string, len(api.TokenKinds)), make([]string, len(api.TokenKinds))
	widths := make([]int, len(api.TokenKinds))
	for at, kind := range api.TokenKinds {
		firsts[at], rests[at], _ = strings.Cut(kind, "_")
		widths[at] = max(len(firsts[at]), len(rests[at]))
	}
	labelWidth := len("tokens")
	for _, row := range rows {
		labelWidth = max(labelWidth, len(row.label))
		for at, count := range row.kinds {
			widths[at] = max(widths[at], utf8.RuneCountInString(count))
		}
	}
	over := gotermboard.Line{gotermboard.C(padRight("tokens", labelWidth), gotermboard.Bold)}
	under := gotermboard.Line{gotermboard.C(padRight("", labelWidth), "")}
	for at := range api.TokenKinds {
		over = append(over, gotermboard.C("  "+padLeft(firsts[at], widths[at]), dimCode))
		under = append(under, gotermboard.C("  "+padLeft(rests[at], widths[at]), dimCode))
	}
	lines := []gotermboard.Line{{}, over, under}
	for _, row := range rows {
		line := gotermboard.Line{gotermboard.C(padRight(row.label, labelWidth), "")}
		for at, count := range row.kinds {
			line = append(line, gotermboard.C("  "+padLeft(count, widths[at]), ""))
		}
		if row.kinds == nil {
			line = append(line, gotermboard.C("  ", ""), row.said)
		}
		lines = append(lines, line)
	}
	return append(lines, notes...)
}

// shortTokensOrNone names each kind's count short, never a total across them, and says none
// reported where no message was.
func shortTokensOrNone(tokens *api.Tokens) string {
	if tokens == nil {
		return "none reported"
	}
	return kindsThenCounts(mapped(tokens.ByKind(), shortCount))
}

// worklogsSnapshot is what --json prints, as tui-worklogs.schema.json describes it.
type worklogsSnapshot struct {
	SchemaVersion      int                  `json:"schema_version"`
	TakenAt            time.Time            `json:"taken_at"`
	Tab                string               `json:"tab"`
	Live               []api.Worklog        `json:"live"`
	Expired            []api.Worklog        `json:"expired"`
	Ended              []api.Worklog        `json:"ended"`
	CursorWorklog      *string              `json:"cursor_worklog"`
	CursorRecordTotals *cursorRecordTotals  `json:"cursor_record_totals"`
	Reads              []gotermboard.Status `json:"reads"`
}

// cursorRecordTotals is what the panel draws of the cursor's worklog's record. Tokens and
// Estimate are `slate totals --json`'s, and Record is that answer's scope.record.
type cursorRecordTotals struct {
	Record   api.RecordSummary      `json:"record"`
	Tokens   *api.Tokens            `json:"tokens"`
	Estimate *api.PreflightEstimate `json:"estimate"`
}

// Snapshot is what --json prints of the tab.
func (b *worklogsBoard) Snapshot(now time.Time) worklogsSnapshot {
	groups := b.groups()
	snapshot := worklogsSnapshot{
		SchemaVersion: worklogsSchemaVersion, TakenAt: now, Tab: tabWorklogs,
		Live: printedWorklogs(groups[0].worklogs), Expired: printedWorklogs(groups[1].worklogs), Ended: printedWorklogs(groups[2].worklogs),
		Reads: []gotermboard.Status{b.live.Status(), b.expired.Status(), b.ended.Status(), b.found.Status(), b.totals.Status()},
	}
	if cursor := b.cursor.Load(); cursor != nil {
		snapshot.CursorWorklog = &cursor.name
		if totals, read := b.totals.Value()[cursor.record]; read && totals.Scope.Record != nil {
			snapshot.CursorRecordTotals = &cursorRecordTotals{Record: *totals.Scope.Record, Tokens: totals.Tokens, Estimate: totals.Estimate}
		}
	}
	return snapshot
}

// printedWorklogs is each worklog with every list it holds printed as a list, as the API answers
// it, where one built here left a list nil.
func printedWorklogs(worklogs []api.Worklog) []api.Worklog {
	printed := make([]api.Worklog, 0, len(worklogs))
	for _, worklog := range worklogs {
		worklog.FormerNames, worklog.Waits, worklog.Spans = listed(worklog.FormerNames), listed(worklog.Waits), listed(worklog.Spans)
		worklog.Links, worklog.DocumentVersions = listed(worklog.Links), listed(worklog.DocumentVersions)
		printed = append(printed, worklog)
	}
	return printed
}
