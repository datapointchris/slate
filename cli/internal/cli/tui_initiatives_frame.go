package cli

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
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

// boardFirstColumnWidth is the room the repos have beside an initiative's counts, past its
// number. A wider one would take cells from the bars.
const boardFirstColumnWidth = 30

// readingWidth is the most a description spreads across, because long lines of prose read badly.
const readingWidth = 100

// tierBarCells leaves a tier's bar room for its percent and its 14-day spark at 120 columns.
const tierBarCells = 20

var boardColumns = slices.Concat(
	[]gotermboard.Column{{Title: "", Min: boardFirstColumnWidth}},
	countColumns,
	[]gotermboard.Column{{Title: "ISSUES"}},
)

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
func (b *initiativesBoard) Frame(now time.Time, grouping string) gotermboard.Frame {
	grouping = cmp.Or(grouping, groupingTier)
	read := b.initiatives.Value()
	frame := gotermboard.Frame{Columns: boardColumns}
	b.places = map[string]queuePlace{}
	if read.ReadTS.IsZero() {
		frame.Strip = []gotermboard.Line{b.stripLine(now, grouping, 0, 0)}
		return frame
	}
	if !read.ReadTS.Equal(b.recordsReadAt) {
		b.records, b.recordsReadAt = recordNumbers(read), read.ReadTS
	}
	ordered, shown := b.orderedAndShown(read)
	showing := 0
	for _, isShown := range shown {
		if isShown {
			showing++
		}
	}
	frame.Strip = []gotermboard.Line{b.stripLine(now, grouping, showing, read.Totals.InitiativeCount), {}}
	frame.StartAt = b.openingKey(read)
	frame.Groups = append(frame.Groups, totalGroup(read, now))
	drawn := boardDraw{text: indexBoardText(b.text.Value()), rows: b.cursorRow.Value(), read: read, shown: shown, now: now}
	switch grouping {
	case groupingRepo:
		repos := b.repoGroups(ordered, drawn)
		if len(repos) > 0 {
			frame.StartAt = repos[0].Key
		}
		frame.Groups = append(frame.Groups, repos...)
	case groupingInitiative:
		frame.Groups = append(frame.Groups, b.initiativeGroups(ordered, "", nil, drawn)...)
	default:
		for _, tier := range read.Tiers {
			if tier.InitiativeCount == 0 {
				continue
			}
			frame.Groups = append(frame.Groups, b.tierGroup(tier, ordered, drawn))
		}
	}
	return frame
}

// boardDraw is what every group of one frame draws from.
type boardDraw struct {
	text  boardText
	rows  cursorRowReads
	read  api.InitiativeBoard
	shown map[int]bool
	now   time.Time
}

func (b *initiativesBoard) stripLine(now time.Time, grouping string, showing, total int) gotermboard.Line {
	line := gotermboard.Line{
		gotermboard.C("slate · initiatives", gotermboard.Bold),
		gotermboard.C("   "+now.Format("15:04:05")+"   ", ""),
		gotermboard.C(b.scope.label(), gotermboard.Cyan),
		gotermboard.C(fmt.Sprintf(" · %d of %d shown   sorted by %s   ", showing, total, boardSorts[b.sort].label), ""),
		gotermboard.C("grouped by "+grouping+"   ", gotermboard.Cyan),
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
		Key:       "total",
		Title:     gotermboard.Line{gotermboard.C("TOTAL · "+strconv.Itoa(read.Totals.InitiativeCount), totalTone)},
		Cells:     toned(boardCells(counts, now), totalTone),
		WrapLast:  wrapped,
		Still:     true,
		Underline: gotermboard.RuleThin,
		Heading:   true,
		Gap:       true,
	}
}

func (b *initiativesBoard) tierGroup(tier api.BoardTier, ordered []api.BoardInitiative, drawn boardDraw) gotermboard.Group {
	var inTier []api.BoardInitiative
	showing := 0
	for _, initiative := range ordered {
		if initiative.Priority != tier.Priority {
			continue
		}
		inTier = append(inTier, initiative)
		if drawn.shown[initiative.Number] {
			showing++
		}
	}
	label := priorityName(tier.Priority) + " · " + strconv.Itoa(tier.InitiativeCount)
	if showing != tier.InitiativeCount {
		label = fmt.Sprintf("%s · %d of %d", priorityName(tier.Priority), showing, tier.InitiativeCount)
	}
	counts := tier.BoardCounts
	return gotermboard.Group{
		Key:        "tier:" + strconv.Itoa(tier.Priority),
		Title:      gotermboard.Line{gotermboard.C(label, tierTone)},
		Cells:      toned(boardCells(counts, drawn.now), tierTone),
		WrapLast:   append(scaledBar(counts), gotermboard.C("  ", ""), gotermboard.C(sparkLine(counts.Closes.ByDay), gotermboard.Green)),
		Heading:    true,
		FilterOnly: showing == 0,
		Gap:        true,
		Overline:   gotermboard.RuleDouble,
		Tone:       tierTone,
		Groups:     b.initiativeGroups(inTier, "", nil, drawn),
		Panel: func(int) []gotermboard.Line {
			return rollupPanel(gotermboard.Line{gotermboard.C(label, tierCode(tier.Priority)+gotermboard.Bold)}, counts, drawn.now)
		},
	}
}

// initiativeGroups draws initiatives in order and places each among the others for the
// reorder keys. Under a repo, issuesIn holds each one's issues in that repo, and nil keeps
// every issue.
func (b *initiativesBoard) initiativeGroups(initiatives []api.BoardInitiative, repo string, issuesIn map[int][]api.BoardIssue, drawn boardDraw) []gotermboard.Group {
	groups := make([]gotermboard.Group, 0, len(initiatives))
	keys := make([]string, 0, len(initiatives))
	places := make([]queuePlace, 0, len(initiatives))
	neighbors := make([]queueNeighbor, 0, len(initiatives))
	for _, initiative := range initiatives {
		key, issues, counts := initiativeKey(initiative.Number), initiative.Issues, initiative.BoardCounts
		if issuesIn != nil {
			key, issues = initiativeUnderRepo(initiative.Number, repo), issuesIn[initiative.Number]
			counts = countsOf(issues, drawn.read)
		}
		hidden := !drawn.shown[initiative.Number]
		groups = append(groups, b.initiativeGroup(initiative, key, issues, counts, hidden, drawn))
		keys = append(keys, key)
		places = append(places, queuePlace{kind: "initiative", number: initiative.Number, priority: initiative.Priority})
		neighbors = append(neighbors, queueNeighbor{number: initiative.Number, title: initiative.Name, priority: initiative.Priority, hidden: hidden})
	}
	b.placeInOrder(keys, places, neighbors)
	return groups
}

func (b *initiativesBoard) initiativeGroup(initiative api.BoardInitiative, key string, issues []api.BoardIssue, counts api.BoardCounts, beyondScope bool, drawn boardDraw) gotermboard.Group {
	long := drawn.text.initiatives[initiative.Number]
	description := strValue(long.Description)
	return gotermboard.Group{
		Key:            key,
		Title:          gotermboard.Line{gotermboard.C(strconv.Itoa(initiative.Number), gotermboard.Cyan)},
		Span:           gotermboard.Line{gotermboard.C(initiative.Name, gotermboard.Bold)},
		Beside:         gotermboard.Items(dimCode, initiative.Repos...),
		Cells:          boardCells(counts, drawn.now),
		WrapLast:       issueBar(counts),
		Under:          gotermboard.C(long.Summary, ""),
		Document:       b.openDescription(initiative.Number, description, len(drawn.text.initiatives) > 0),
		Folded:         true,
		Rule:           true,
		BlankAboveRule: true,
		FilterOnly:     beyondScope,
		Groups:         b.issueGroups(strings.TrimPrefix(key, initiativeKeyPrefix), issues, drawn),
		Text:           description + " " + strValue(long.StatusReason),
		Panel: func(width int) []gotermboard.Line {
			return b.initiativePanel(initiative, long, drawn.rows, drawn.read, drawn.now, width)
		},
	}
}

// noRepo is the group holding the issues that name no repo.
const noRepo = "no repo"

// repoGroups is a group for each repo the issues name, the most open first, holding the
// initiatives with an issue there, each cut to its issues there. A repo's counts sum every
// initiative's issues there whatever the scope shows, as a tier's do.
func (b *initiativesBoard) repoGroups(ordered []api.BoardInitiative, drawn boardDraw) []gotermboard.Group {
	inRepo := map[string][]api.BoardIssue{}
	byInitiative := map[string]map[int][]api.BoardIssue{}
	for _, initiative := range ordered {
		for _, issue := range initiative.Issues {
			repo := cmp.Or(strValue(issue.Repo), noRepo)
			inRepo[repo] = append(inRepo[repo], issue)
			if byInitiative[repo] == nil {
				byInitiative[repo] = map[int][]api.BoardIssue{}
			}
			byInitiative[repo][initiative.Number] = append(byInitiative[repo][initiative.Number], issue)
		}
	}
	counts := make(map[string]api.BoardCounts, len(inRepo))
	repos := slices.Collect(maps.Keys(inRepo))
	for _, repo := range repos {
		counts[repo] = countsOf(inRepo[repo], drawn.read)
	}
	slices.SortFunc(repos, func(x, y string) int {
		return cmp.Or(cmp.Compare(boolRank(x == noRepo), boolRank(y == noRepo)), cmp.Compare(counts[y].OpenCount, counts[x].OpenCount), strings.Compare(x, y))
	})
	groups := make([]gotermboard.Group, 0, len(repos))
	for _, repo := range repos {
		var initiatives []api.BoardInitiative
		showing := 0
		for _, initiative := range ordered {
			if _, there := byInitiative[repo][initiative.Number]; there {
				initiatives = append(initiatives, initiative)
				if drawn.shown[initiative.Number] {
					showing++
				}
			}
		}
		label := repo + " · " + strconv.Itoa(len(initiatives))
		if showing != len(initiatives) {
			label = fmt.Sprintf("%s · %d of %d", repo, showing, len(initiatives))
		}
		repoCounts := counts[repo]
		groups = append(groups, gotermboard.Group{
			Key:        "repo:" + repo,
			Title:      gotermboard.Line{gotermboard.C(label, tierTone)},
			Cells:      toned(boardCells(repoCounts, drawn.now), tierTone),
			WrapLast:   append(scaledBar(repoCounts), gotermboard.C("  ", ""), gotermboard.C(sparkLine(repoCounts.Closes.ByDay), gotermboard.Green)),
			Heading:    true,
			FilterOnly: showing == 0,
			Gap:        true,
			Overline:   gotermboard.RuleDouble,
			Tone:       tierTone,
			Groups:     b.initiativeGroups(initiatives, repo, byInitiative[repo], drawn),
			Panel: func(int) []gotermboard.Line {
				return rollupPanel(heading(label), repoCounts, drawn.now)
			},
		})
	}
	return groups
}

func boolRank(last bool) int {
	if last {
		return 1
	}
	return 0
}

// countsOf sums issues as the API sums an initiative's: each issue in its one state, and a
// completed one's close counted back from the read's moment on the read's calendar.
func countsOf(issues []api.BoardIssue, read api.InitiativeBoard) api.BoardCounts {
	zone, err := time.LoadLocation(read.Zone)
	if err != nil {
		zone = time.UTC
	}
	counts := api.BoardCounts{IssueCount: len(issues)}
	onDay := map[string]int{}
	closes := &counts.Closes
	for _, issue := range issues {
		countState(&counts.States, issue.State)
		if issue.State != api.IssueStatusCompleted || issue.ClosedTS == nil {
			continue
		}
		at := *issue.ClosedTS
		for _, window := range []struct {
			within time.Duration
			count  *int
		}{{24 * time.Hour, &closes.Last24h}, {7 * 24 * time.Hour, &closes.Last7d}, {30 * 24 * time.Hour, &closes.Last30d}} {
			if at.After(read.ReadTS.Add(-window.within)) {
				*window.count++
			}
		}
		if closes.LastClosedTS == nil || at.After(*closes.LastClosedTS) {
			closes.LastClosedTS = &at
		}
		onDay[at.In(zone).Format(time.DateOnly)]++
	}
	counts.OpenCount = counts.IssueCount - counts.States.Completed - counts.States.Canceled
	for _, day := range read.Days {
		closes.ByDay = append(closes.ByDay, onDay[day])
	}
	return counts
}

func countState(states *api.IssueStateCounts, state string) {
	switch state {
	case api.IssueStatusCompleted:
		states.Completed++
	case api.IssueStatusCanceled:
		states.Canceled++
	case api.BoardStateRunning:
		states.Running++
	case api.BoardStateWaiting:
		states.Waiting++
	case api.BoardStateBlocked:
		states.Blocked++
	case api.BoardStateOpenChildren:
		states.OpenChildren++
	case api.IssueStatusTriage:
		states.Triage++
	case "deferred":
		states.Deferred++
	case "shelved":
		states.Shelved++
	case "ready":
		states.Ready++
	}
}

// openDescription is the description under the row while d holds it open.
func (b *initiativesBoard) openDescription(number int, description string, read bool) gotermboard.Document {
	if !b.described[number] {
		return nil
	}
	return func(width int) []gotermboard.Line {
		switch {
		case !read:
			return []gotermboard.Line{dimLine("reading…")}
		case strings.TrimSpace(description) == "":
			return []gotermboard.Line{dimLine("No description.")}
		}
		return slices.Concat(b.descriptionLines(description, width), []gotermboard.Line{dimLine("d closes the description")})
	}
}

func (b *initiativesBoard) descriptionLines(description string, width int) []gotermboard.Line {
	return b.markdown.Lines(description, min(width, readingWidth))
}

// readTallDescription opens a record's description on the reader where it runs past the
// screen, and says whether it did. An issue's is read with the cursor's row.
func (b *initiativesBoard) readTallDescription(m *gotermboard.Model, number int) bool {
	var description, title string
	if long, read := indexBoardText(b.text.Value()).initiatives[number]; read {
		description = strValue(long.Description)
	} else if issue, read := b.cursorRow.Value().issues[number]; read {
		description = strValue(issue.Description)
	} else {
		return false
	}
	if b.described[number] {
		return false
	}
	document := func(width int) []gotermboard.Line { return b.descriptionLines(description, width) }
	if !m.Tall(document) {
		return false
	}
	_, _, title, _ = b.recordAt(initiativeKey(number))
	if title == "" {
		_, _, title, _ = b.recordAt(issueKey(number))
	}
	m.Read(strconv.Itoa(number)+"  "+title, document)
	return true
}

// recordNumbers is every record a frame can name, so prose colors only a number that is one.
func recordNumbers(read api.InitiativeBoard) map[int]bool {
	numbers := map[int]bool{}
	for _, initiative := range read.Initiatives {
		numbers[initiative.Number] = true
		for _, issue := range initiative.Issues {
			numbers[issue.Number] = true
		}
	}
	return numbers
}

// numberInProse is a run of digits as long as a record's number can read in a sentence.
var numberInProse = regexp.MustCompile(`#?\b\d{3,6}\b`)

// colorRecordNumbers draws a number in prose in the color the board draws records in, where
// it names one. A number inside a date, a version or a time names none, whatever its digits.
func (b *initiativesBoard) colorRecordNumbers(text, code string) gotermboard.Line {
	var line gotermboard.Line
	at := 0
	for _, match := range numberInProse.FindAllStringIndex(text, -1) {
		number, err := strconv.Atoi(strings.TrimPrefix(text[match[0]:match[1]], "#"))
		if err != nil || !b.records[number] || insideALargerValue(text, match[0], match[1]) {
			continue
		}
		if match[0] > at {
			line = append(line, gotermboard.C(text[at:match[0]], code))
		}
		line = append(line, gotermboard.C(text[match[0]:match[1]], gotermboard.Cyan))
		at = match[1]
	}
	if at < len(text) {
		line = append(line, gotermboard.C(text[at:], code))
	}
	return line
}

func insideALargerValue(text string, from, to int) bool {
	if from > 0 && strings.ContainsRune(".-/:", rune(text[from-1])) {
		return true
	}
	if to == len(text) {
		return false
	}
	switch text[to] {
	case '-', '/', ':', '%':
		return true
	case '.':
		return to+1 < len(text) && text[to+1] >= '0' && text[to+1] <= '9'
	}
	return false
}

// issueGroups is an initiative's issues unfolded: the open first, running at the top and
// then in queue order, with the done and the canceled folded beneath.
// The open issues are placed for the reorder keys in the order they draw. under is the
// initiative's number, with its repo where the repo grouping draws it.
func (b *initiativesBoard) issueGroups(under string, issues []api.BoardIssue, drawn boardDraw) []gotermboard.Group {
	var running, waiting, queued, done, canceled []api.BoardIssue
	for _, issue := range issues {
		switch issue.State {
		case api.BoardStateRunning:
			running = append(running, issue)
		case api.BoardStateWaiting:
			waiting = append(waiting, issue)
		case api.IssueStatusCompleted:
			done = append(done, issue)
		case api.IssueStatusCanceled:
			canceled = append(canceled, issue)
		default:
			queued = append(queued, issue)
		}
	}
	var groups []gotermboard.Group
	if open := slices.Concat(running, waiting, queued); len(open) > 0 {
		keys := make([]string, len(open))
		places := make([]queuePlace, len(open))
		neighbors := make([]queueNeighbor, len(open))
		for at, issue := range open {
			keys[at] = issueKey(issue.Number)
			places[at] = queuePlace{kind: "issue", number: issue.Number, priority: issue.EffectivePriority}
			neighbors[at] = queueNeighbor{number: issue.Number, title: issue.Title, priority: issue.EffectivePriority}
		}
		b.placeInOrder(keys, places, neighbors)
		groups = append(groups, gotermboard.Group{
			Key:     "open:" + under,
			Title:   gotermboard.Line{gotermboard.C("open · "+strconv.Itoa(len(open)), gotermboard.Bold)},
			Summary: gotermboard.Line{gotermboard.C("running first, then in queue order", dimCode)},
			Still:   true,
			Heading: true,
			Groups:  b.issueGroupsOf(open, drawn),
		})
	}
	if len(done) > 0 {
		groups = append(groups, gotermboard.Group{
			Key:     "done:" + under,
			Title:   gotermboard.Line{gotermboard.C("done · "+strconv.Itoa(len(done)), gotermboard.Green)},
			Summary: gotermboard.Line{gotermboard.C("newest first", dimCode)},
			Heading: true,
			Folded:  true,
			Groups:  b.issueGroupsOf(done, drawn),
		})
	}
	if len(canceled) > 0 {
		groups = append(groups, gotermboard.Group{
			Key:     "canceled:" + under,
			Title:   gotermboard.Line{gotermboard.C("canceled · "+strconv.Itoa(len(canceled)), "")},
			Heading: true,
			Folded:  true,
			Groups:  b.issueGroupsOf(canceled, drawn),
		})
	}
	return groups
}

func (b *initiativesBoard) issueGroupsOf(issues []api.BoardIssue, drawn boardDraw) []gotermboard.Group {
	groups := make([]gotermboard.Group, len(issues))
	for at, issue := range issues {
		groups[at] = b.issueGroup(issue, drawn)
	}
	return groups
}

// issueGroup is an issue that unfolds into the sessions and pull requests its worklogs hold,
// which are read once the cursor lands on it. d opens its description.
func (b *initiativesBoard) issueGroup(issue api.BoardIssue, drawn boardDraw) gotermboard.Group {
	excerpt := drawn.text.issues[issue.Number]
	read, isRead := drawn.rows.issues[issue.Number]
	worklogs, worked := drawn.rows.worklogs[issue.Number]
	description := strValue(read.Description)
	lead := gotermboard.C(padRight(boardStateWord(issue.State), issueStateWidth)+" ", issueStateCode(issue.State))
	if issue.ClosedTS != nil {
		lead = gotermboard.Ticking(padLeft(ageOf(issue.ClosedTS, drawn.now), issueStateWidth)+" ", dimCode)
	}
	group := gotermboard.Group{
		Key:      issueKey(issue.Number),
		Title:    gotermboard.Line{lead, gotermboard.C(strconv.Itoa(issue.Number), gotermboard.Cyan), gotermboard.C("  "+issue.Title, "")},
		Under:    gotermboard.C(excerpt, ""),
		Document: b.openDescription(issue.Number, description, isRead),
		Folded:   true,
		Rows:     workRows(issue.Number, worklogs, worked, drawn.now),
		Text:     description,
		Panel: func(width int) []gotermboard.Line {
			return b.issuePanel(issue, excerpt, read, isRead, worklogs, worked, drawn.now, width)
		},
	}
	if worked {
		group.Summary = gotermboard.Line{gotermboard.C(workCount(worklogs), dimCode)}
	}
	return group
}

// workRows is a pull request row for each one an issue's worklogs link, then a row for each
// session in each, newest worklog first.
func workRows(issue int, worklogs []api.Worklog, worked bool, now time.Time) []gotermboard.Row {
	placeholder := func(text string) []gotermboard.Row {
		return []gotermboard.Row{{Key: "work:" + strconv.Itoa(issue), Span: gotermboard.Line{gotermboard.C(text, dimCode)}}}
	}
	if !worked {
		return placeholder("reading…")
	}
	var rows []gotermboard.Row
	for _, worklog := range worklogs {
		for _, link := range worklog.Links {
			if link.Kind == api.WorklogLinkPullRequest {
				rows = append(rows, pullRequestRow(issue, worklog, link, now))
			}
		}
	}
	for _, worklog := range worklogs {
		for _, session := range sessionsIn(worklog) {
			rows = append(rows, sessionRow(issue, worklog, session, now))
		}
	}
	if len(rows) == 0 {
		return placeholder("No sessions or pull requests yet.")
	}
	return rows
}

func pullRequestRow(issue int, worklog api.Worklog, link api.WorklogLink, now time.Time) gotermboard.Row {
	name := pullRequestName(link.Ref)
	return gotermboard.Row{
		Key: pullRequestKey(issue, link.Ref),
		Span: gotermboard.Line{
			gotermboard.C(padRight("PR", issueStateWidth)+" ", gotermboard.Magenta),
			gotermboard.C(name, gotermboard.Cyan),
			gotermboard.C(fmt.Sprintf("  %s · linked %s ago", worklog.Name, ageOf(&link.LinkedTS, now)), dimCode),
		},
		Text: link.Ref,
		Panel: func(int) []gotermboard.Line {
			return []gotermboard.Line{
				heading(name),
				plainLine(link.Ref),
				{},
				plainLine(fmt.Sprintf("in %s, linked %s ago by %s", worklog.Name, ageOf(&link.LinkedTS, now), link.LinkedBy)),
				{},
				dimLine("enter opens it in prs, and o in the browser"),
			}
		},
	}
}

// workedSession is one session's spans in one worklog, summed.
type workedSession struct {
	id        string
	directory string
	started   time.Time
	ended     *time.Time
	tokens    *api.Tokens
}

// sessionsIn is each session that joined the worklog, in the order each first joined. A
// session that left and joined again is one, ended only where its every span is.
func sessionsIn(worklog api.Worklog) []workedSession {
	var sessions []workedSession
	at := map[string]int{}
	for _, span := range worklog.Spans {
		i, seen := at[span.Session]
		if !seen {
			at[span.Session] = len(sessions)
			sessions = append(sessions, workedSession{id: span.Session, directory: strValue(span.Directory), started: span.StartedTS, ended: span.EndedTS})
			i = len(sessions) - 1
		}
		session := &sessions[i]
		switch {
		case span.EndedTS == nil:
			session.ended = nil
		case session.ended != nil && span.EndedTS.After(*session.ended):
			session.ended = span.EndedTS
		}
		if span.Tokens != nil {
			if session.tokens == nil {
				session.tokens = &api.Tokens{}
			}
			session.tokens.UncachedInput += span.Tokens.UncachedInput
			session.tokens.CacheWrite += span.Tokens.CacheWrite
			session.tokens.CacheRead += span.Tokens.CacheRead
			session.tokens.Output += span.Tokens.Output
		}
	}
	return sessions
}

func sessionRow(issue int, worklog api.Worklog, session workedSession, now time.Time) gotermboard.Row {
	when := "since " + ageOf(&session.started, now) + " ago"
	if session.ended != nil {
		when = fmt.Sprintf("%s ago for %s", ageOf(session.ended, now), gotermboard.Ago(session.ended.Sub(session.started)))
	}
	lines := []gotermboard.Line{
		plainLine("session " + session.id),
		plainLine(fmt.Sprintf("in %s from %s", worklog.Name, session.started.Local().Format("2006-01-02 15:04"))),
	}
	if session.directory != "" {
		lines = append(lines, dimLine(session.directory))
	}
	if session.tokens != nil {
		lines = append(lines, plainLine(fmt.Sprintf("uncached input %s · cache write %s · cache read %s · output %s",
			groupedDigits(session.tokens.UncachedInput), groupedDigits(session.tokens.CacheWrite),
			groupedDigits(session.tokens.CacheRead), groupedDigits(session.tokens.Output))))
	}
	return gotermboard.Row{
		Key: fmt.Sprintf("session:%d:%s:%s", issue, worklog.Name, session.id),
		Span: gotermboard.Line{
			gotermboard.C(padRight("session", issueStateWidth)+" ", ""),
			gotermboard.C(shortSession(session.id), ""),
			gotermboard.C(fmt.Sprintf("  %s · %s", worklog.Name, when), dimCode),
		},
		Text:     session.id + " " + session.directory,
		Document: func(int) []gotermboard.Line { return lines },
	}
}

// shortSession is a session id's first group, as a person tells sessions apart.
func shortSession(id string) string {
	first, _, _ := strings.Cut(id, "-")
	return first
}

// workCount is what a folded issue says its worklogs hold.
func workCount(worklogs []api.Worklog) string {
	sessions, pullRequests := 0, 0
	for _, worklog := range worklogs {
		sessions += len(sessionsIn(worklog))
		for _, link := range worklog.Links {
			if link.Kind == api.WorklogLinkPullRequest {
				pullRequests++
			}
		}
	}
	return fmt.Sprintf("%d %s · %d %s", sessions, plural(sessions, "session", "sessions"),
		pullRequests, plural(pullRequests, "pull request", "pull requests"))
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

// countColumns titles boardCells' cells, in its order.
var countColumns = []gotermboard.Column{
	{Title: "DONE", Right: true},
	{Title: "24H", Right: true},
	{Title: "7D", Right: true},
	{Title: "30D", Right: true},
	{Title: "LAST", Right: true},
	{Title: "RUN", Right: true},
	{Title: "BLK", Right: true},
	{Title: "RDY", Right: true},
}

// boardCells is a row's cells on countColumns.
func boardCells(counts api.BoardCounts, now time.Time) []gotermboard.Cell {
	done := counts.States.Completed
	underWay, _, _ := openKinds(counts.States, counts.OpenCount)
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
		countCell(underWay, gotermboard.Yellow),
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

// issueBar has one cell per issue, so a count on it can be checked by eye. Canceled
// issues stay off it.
func issueBar(counts api.BoardCounts) gotermboard.Line {
	underWay, blocked, rest := openKinds(counts.States, counts.OpenCount)
	return gotermboard.TallyBar(counts.States.Completed, underWay, blocked, rest)
}

// scaledBar is issueBar's kinds shared over tierBarCells, for a row holding too many
// issues to draw one cell each. A kind holding any issue keeps a cell, so a blocked issue
// never rounds away.
func scaledBar(counts api.BoardCounts) gotermboard.Line {
	underWay, blocked, rest := openKinds(counts.States, counts.OpenCount)
	kinds := []int{counts.States.Completed, underWay, blocked, rest}
	total := 0
	for _, n := range kinds {
		total += n
	}
	if total == 0 {
		return gotermboard.TallyBar(0, 0, 0, tierBarCells)
	}
	cells := make([]int, len(kinds))
	left := tierBarCells
	for i, n := range kinds {
		cells[i] = n * tierBarCells / total
		if n > 0 {
			cells[i] = max(cells[i], 1)
		}
		left -= cells[i]
	}
	widest := slices.Index(cells, slices.Max(cells))
	cells[widest] += left
	return gotermboard.TallyBar(cells[0], cells[1], cells[2], cells[3])
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

// totalTone and tierTone are the codes a heading's whole line is drawn in.
const (
	totalTone = "\033[1;97m"
	tierTone  = gotermboard.BoldYellow
)

func toned(cells []gotermboard.Cell, code string) []gotermboard.Cell {
	for i := range cells {
		cells[i].Code = code
	}
	return cells
}
