package cli

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/datapointchris/gotermboard"
	"github.com/datapointchris/gotermboard/chart"

	"github.com/datapointchris/slate/cli/internal/api"
)

// noRepoLabel names the row of the issues on no repo.
const noRepoLabel = "(no repo)"

func repoLabel(repo *string) string {
	if repo == nil {
		return noRepoLabel
	}
	return *repo
}

// repoFirstColumnWidth fits most repo names whole.
const repoFirstColumnWidth = 24

// repoChartHeight is each chart's lines in the panel: its bars, its axis and its labels.
const repoChartHeight = 9

// panelReadyRows is how many of the longest-waiting ready issues the panel lists.
const panelReadyRows = 5

var repoColumns = []gotermboard.Column{
	{Title: "", Min: repoFirstColumnWidth},
	{Title: "DONE", Right: true},
	{Title: "24H", Right: true},
	{Title: "7D", Right: true},
	{Title: "30D", Right: true},
	{Title: "LAST", Right: true},
	{Title: "RUN", Right: true},
	{Title: "BLK", Right: true},
	{Title: "RDY", Right: true},
	{Title: "WAIT", Right: true},
	{Title: "12 WEEKS"},
	{Title: "ISSUES"},
}

// readyWait is one span of the histogram of how long ready issues have waited, up to under,
// in a code that turns warm as the wait grows.
type readyWait struct {
	label string
	under time.Duration
	code  string
}

const day = 24 * time.Hour

var readyWaits = []readyWait{
	{label: "<1d", under: day},
	{label: "1-3d", under: 3 * day},
	{label: "3-7d", under: 7 * day},
	{label: "1-2w", under: 14 * day, code: gotermboard.Yellow},
	{label: "2-4w", under: 28 * day, code: gotermboard.Yellow},
	{label: "1-3mo", under: 91 * day, code: gotermboard.Red},
	{label: "3mo+", under: math.MaxInt64, code: gotermboard.Red},
}

func waitOf(waited time.Duration) readyWait {
	for _, wait := range readyWaits {
		if waited < wait.under {
			return wait
		}
	}
	return readyWaits[len(readyWaits)-1]
}

// Frame is gotermboard.Board's.
func (b *reposBoard) Frame(now time.Time, _ string) gotermboard.Frame {
	read := b.repos.Value()
	frame := gotermboard.Frame{Columns: repoColumns}
	if read.ReadTS.IsZero() {
		frame.Strip = []gotermboard.Line{b.stripLine(now, 0)}
		return frame
	}
	frame.Strip = []gotermboard.Line{b.stripLine(now, len(read.Repos)), {}}
	var working, quiet []gotermboard.Row
	for _, repo := range b.ordered(read) {
		row := repoRow(repo, read, now)
		if repo.OpenCount == 0 && sum(repo.ClosesByWeek) == 0 {
			quiet = append(quiet, row)
			continue
		}
		working = append(working, row)
	}
	frame.Groups = append(frame.Groups, reposTotalGroup(read, now), gotermboard.Group{
		Key:     "working",
		Title:   gotermboard.Line{gotermboard.C("with work · "+strconv.Itoa(len(working)), tierTone)},
		Summary: gotermboard.Line{gotermboard.C("an open issue or a close in 12 weeks", dimCode)},
		Heading: true,
		Still:   true,
		Gap:     true,
		Rows:    working,
	})
	if len(quiet) > 0 {
		frame.Groups = append(frame.Groups, gotermboard.Group{
			Key:     "quiet",
			Title:   gotermboard.Line{gotermboard.C("quiet · "+strconv.Itoa(len(quiet)), "")},
			Summary: gotermboard.Line{gotermboard.C("nothing open and no close in 12 weeks", dimCode)},
			Heading: true,
			Folded:  true,
			Rows:    quiet,
		})
	}
	return frame
}

func (b *reposBoard) stripLine(now time.Time, repos int) gotermboard.Line {
	line := gotermboard.Line{
		gotermboard.C("slate · repos", gotermboard.Bold),
		gotermboard.C("   "+now.Format("15:04:05")+"   ", ""),
		gotermboard.C(fmt.Sprintf("%d repos   sorted by %s   ", repos, repoSorts[b.sort].label), ""),
	}
	return append(line, gotermboard.ReadStatus([]gotermboard.Status{b.repos.Status()}, now)...)
}

func reposTotalGroup(read api.RepoBoard, now time.Time) gotermboard.Group {
	var ready []readyInRepo
	for _, repo := range read.Repos {
		for _, issue := range repo.Ready {
			ready = append(ready, readyInRepo{issue: issue, repo: repoLabel(repo.Repo)})
		}
	}
	sortReady(ready)
	title := "TOTAL · " + strconv.Itoa(len(read.Repos)) + " repos"
	return gotermboard.Group{
		Key:       "total",
		Title:     gotermboard.Line{gotermboard.C(title, totalTone)},
		Cells:     toned(repoCells(read.Totals, oldestReady(ready), now), totalTone),
		WrapLast:  percentBar(read.Totals.BoardCounts),
		Underline: gotermboard.RuleThin,
		Heading:   true,
		Gap:       true,
		Panel: func(width int) []gotermboard.Line {
			return repoPanel("Every repo", read.Totals, ready, read, now, width)
		},
	}
}

func repoRow(repo api.BoardRepo, read api.RepoBoard, now time.Time) gotermboard.Row {
	ready := make([]readyInRepo, 0, len(repo.Ready))
	for _, issue := range repo.Ready {
		ready = append(ready, readyInRepo{issue: issue})
	}
	name := gotermboard.C(repoLabel(repo.Repo), gotermboard.Bold)
	if repo.Repo == nil {
		name.Code = dimCode
	}
	return gotermboard.Row{
		Key:      "repo:" + repoLabel(repo.Repo),
		Cells:    append([]gotermboard.Cell{name}, repoCells(repo.RepoRollup, oldestReady(ready), now)...),
		WrapLast: percentBar(repo.BoardCounts),
		Panel: func(width int) []gotermboard.Line {
			return repoPanel(repoLabel(repo.Repo), repo.RepoRollup, ready, read, now, width)
		},
	}
}

// repoCells is boardCells, then how long the oldest ready issue has waited and a spark of
// the closes per week.
func repoCells(rollup api.RepoRollup, oldest *api.ReadyIssue, now time.Time) []gotermboard.Cell {
	wait := gotermboard.C("·", dimCode)
	if oldest != nil {
		waited := now.Sub(oldest.CreatedTS)
		wait = gotermboard.Ticking(gotermboard.Ago(waited), waitOf(waited).code)
	}
	return append(boardCells(rollup.BoardCounts, now), wait, gotermboard.C(sparkLine(rollup.ClosesByWeek), gotermboard.Green))
}

func percentBar(counts api.BoardCounts) gotermboard.Line {
	done, all := counts.States.Completed, counts.States.Completed+counts.OpenCount
	percent := 0
	if all > 0 {
		percent = (100*done + all/2) / all
	}
	return append(scaledBar(counts), gotermboard.C(fmt.Sprintf(" %d%%", percent), ""))
}

// readyInRepo is a ready issue, and its repo where a panel lists several repos' issues.
type readyInRepo struct {
	issue api.ReadyIssue
	repo  string
}

func sortReady(ready []readyInRepo) {
	slices.SortStableFunc(ready, func(a, b readyInRepo) int { return a.issue.CreatedTS.Compare(b.issue.CreatedTS) })
}

func oldestReady(ready []readyInRepo) *api.ReadyIssue {
	if len(ready) == 0 {
		return nil
	}
	return &ready[0].issue
}

func repoPanel(title string, rollup api.RepoRollup, ready []readyInRepo, read api.RepoBoard, now time.Time, width int) []gotermboard.Line {
	lines := []gotermboard.Line{heading(title), {}, stateCountsCells(rollup.States)}
	if rollup.States.Canceled > 0 {
		lines = append(lines, plainLine(strconv.Itoa(rollup.States.Canceled)+" canceled, not on the bar"))
	}
	lines = append(lines, gotermboard.Line{})
	lines = append(lines, closesLines(rollup.Closes, now)...)
	lines = append(lines, gotermboard.Line{}, heading(fmt.Sprintf("Closed per week, last %d weeks · %d", len(read.Weeks), sum(rollup.ClosesByWeek))))
	if sum(rollup.ClosesByWeek) == 0 {
		lines = append(lines, dimLine("No closes in these weeks."))
	} else {
		lines = append(lines, weeklyCloses(rollup.ClosesByWeek, read.Weeks).Lines(width, repoChartHeight)...)
	}
	lines = append(lines, gotermboard.Line{}, heading("Ready, by how long each has waited · "+strconv.Itoa(len(ready))))
	if len(ready) == 0 {
		return append(lines, dimLine("Nothing is ready."))
	}
	lines = append(lines, readyHistogram(ready, now).Lines(width, repoChartHeight)...)
	lines = append(lines, gotermboard.Line{}, heading("Waiting longest"))
	for at, row := range ready {
		if at == panelReadyRows {
			break
		}
		waited := now.Sub(row.issue.CreatedTS)
		line := gotermboard.Line{
			gotermboard.C(padLeft(gotermboard.Ago(waited), 4)+"  ", waitOf(waited).code),
			gotermboard.C(strconv.Itoa(row.issue.Number), gotermboard.Cyan),
		}
		if row.repo != "" {
			line = append(line, gotermboard.C("  "+row.repo, dimCode))
		}
		lines = append(lines, append(line, gotermboard.C("  "+row.issue.Title, "")))
	}
	return lines
}

// weeklyCloses is a column per week. Each is labeled by its Monday's day of the month, by its
// month where the month turns, and the week of the read as now, because a bar is often three
// columns wide.
func weeklyCloses(counts []int, weeks []string) chart.Bars {
	bars := make([]chart.Bar, 0, len(counts))
	month := ""
	for at, count := range counts {
		label := ""
		if at < len(weeks) {
			if monday, err := time.Parse(time.DateOnly, weeks[at]); err == nil {
				label = strconv.Itoa(monday.Day())
				if monday.Format("Jan") != month {
					month = monday.Format("Jan")
					label = month
				}
			}
		}
		if at == len(counts)-1 {
			label = "now"
		}
		bars = append(bars, chart.Bar{Label: label, Value: float64(count), Code: gotermboard.Green})
	}
	return chart.Bars{Bars: bars, ValueLabel: wholeCount}
}

// wholeCount labels a count's axis, whose midpoint can fall between two counts.
func wholeCount(v float64) string { return chart.Compact(math.Round(v)) }

// readyHistogram counts the ready issues in each span of readyWaits.
func readyHistogram(ready []readyInRepo, now time.Time) chart.Bars {
	counts := make(map[string]int, len(readyWaits))
	for _, row := range ready {
		counts[waitOf(now.Sub(row.issue.CreatedTS)).label]++
	}
	bars := make([]chart.Bar, 0, len(readyWaits))
	for _, wait := range readyWaits {
		bars = append(bars, chart.Bar{Label: wait.label, Value: float64(counts[wait.label]), Code: wait.code})
	}
	return chart.Bars{Bars: bars, ValueLabel: wholeCount}
}

func sum(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}
