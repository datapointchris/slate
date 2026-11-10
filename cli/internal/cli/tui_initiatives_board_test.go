package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/gotermboard"
	"github.com/datapointchris/gotermstyle"

	"github.com/datapointchris/slate/cli/internal/api"
)

// boardFromServer is a board that has read initiatives from a stand-in for the API.
func boardFromServer(t *testing.T, read api.InitiativeBoard, scope boardScope) *initiativesBoard {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/initiatives/board":
			_ = json.NewEncoder(w).Encode(read)
		case "/api/v1/initiatives/progress":
			_, _ = w.Write([]byte(`[]`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(server.Close)
	board := newInitiativesBoard(api.New(server.URL, server.Client()), "", scope, "")
	if err := board.readOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	return board
}

// sevenHighAndTwoLow is a board of nine initiatives numbered in list order.
func sevenHighAndTwoLow() api.InitiativeBoard {
	read := api.InitiativeBoard{ReadTS: time.Now(), Tiers: []api.BoardTier{{Priority: 2}, {Priority: 4}, {Priority: 0}}}
	read.Totals.InitiativeCount = 9
	read.Tiers[0].InitiativeCount, read.Tiers[1].InitiativeCount = 7, 2
	for number := 1; number <= 9; number++ {
		priority := 2
		if number > 7 {
			priority = 4
		}
		read.Initiatives = append(read.Initiatives, api.BoardInitiative{Number: number, Name: "initiative " + strconv.Itoa(number), Status: "active", Priority: priority})
	}
	return read
}

func TestAScopeCutsTheBoardInTheClient(t *testing.T) {
	for _, test := range []struct {
		scope  boardScope
		shown  []int
		tier   string
		hidden string
	}{
		{boardScope{kind: scopePerTier, perTier: 5}, []int{1, 2, 3, 4, 5, 8, 9}, "high · 5 of 7", "none ·"},
		{boardScope{kind: scopeFirstInList, first: 3}, []int{1, 2, 3}, "high · 3 of 7", "low ·"},
		{boardScope{kind: scopeEveryInitiative}, []int{1, 2, 3, 4, 5, 6, 7, 8, 9}, "high · 7", "none ·"},
	} {
		board := boardFromServer(t, sevenHighAndTwoLow(), test.scope)
		if got := board.Snapshot(time.Now()).Shown; !slices.Equal(got, test.shown) {
			t.Errorf("%s shows %v, want %v", test.scope.label(), got, test.shown)
		}
		screen := gotermboard.Once(board, gotermstyle.NewPalette(false), 120, 40)
		if !strings.Contains(screen, test.tier) || !strings.Contains(screen, "TOTAL · 9") {
			t.Errorf("%s should label its tier %q under the whole total:\n%s", test.scope.label(), test.tier, screen)
		}
		if strings.Contains(screen, test.hidden) {
			t.Errorf("%s draws a tier showing nothing, %q:\n%s", test.scope.label(), test.hidden, screen)
		}
	}
}

func TestANumberInProseColorsOnlyWhereItNamesARecord(t *testing.T) {
	board := &initiativesBoard{records: map[int]bool{1888: true, 2026: true}}
	for text, want := range map[string]string{
		"ahead of 1888.":       "1888",
		"see #1888, then 2026": "#18882026",
		"set on 2026-11-03":    "",
		"at v1.2026":           "",
		"grew 1888%":           "",
		"run 799 failed":       "",
	} {
		var colored strings.Builder
		line := board.colorRecordNumbers(text, "")
		for _, cell := range line {
			if cell.Code == gotermboard.Cyan {
				colored.WriteString(cell.Text)
			}
		}
		if colored.String() != want {
			t.Errorf("%q colored %q, want %q", text, colored.String(), want)
		}
		if line.Text() != text {
			t.Errorf("%q drew as %q", text, line.Text())
		}
	}
}

func TestABarHasOneCellPerIssueAndLeavesTheCanceledOff(t *testing.T) {
	counts := api.BoardCounts{
		IssueCount: 50, OpenCount: 29,
		States: api.IssueStateCounts{Completed: 19, Canceled: 2, Running: 3, Waiting: 11, Blocked: 8, Ready: 7},
	}
	cells := func(line gotermboard.Line) []int {
		widths := make([]int, len(line))
		for i, cell := range line {
			widths[i] = len([]rune(cell.Text))
		}
		return widths
	}
	if got, want := cells(issueBar(counts)), []int{19, 14, 8, 7}; !slices.Equal(got, want) {
		t.Errorf("done, under way, blocked and the rest take %v cells, want %v", got, want)
	}
	total := 0
	scaled := cells(scaledBar(counts))
	for _, width := range scaled {
		total += width
	}
	if total != tierBarCells || slices.Contains(scaled, 0) {
		t.Errorf("a scaled bar takes %v cells, want %d in all with none of the kinds lost", scaled, tierBarCells)
	}
}

func TestUnderTheRepoGroupingAReorderNamesTheInitiativesItPassesThatTheGroupDoesNotDraw(t *testing.T) {
	read := sevenHighAndTwoLow()
	apiRepo, cliRepo := "api", "cli"
	cut := func(repo *string) []api.BoardRepoCounts { return []api.BoardRepoCounts{{Repo: repo}} }
	for at := range read.Initiatives {
		read.Initiatives[at].ByRepo = cut(nil)
	}
	read.Initiatives[2].ByRepo, read.Initiatives[3].ByRepo, read.Initiatives[4].ByRepo = cut(&apiRepo), cut(&cliRepo), cut(&apiRepo)
	read.Repos = []api.BoardRepoRollup{{Repo: &apiRepo}, {Repo: &cliRepo}, {}}
	board := boardFromServer(t, read, boardScope{kind: scopeEveryInitiative})
	board.Frame(time.Now(), groupingRepo)
	step := board.places[initiativeKey(3)+placedUnder("api")].below
	if step == nil || step.to.number != 5 || len(step.passes) != 1 || step.passes[0].number != 4 {
		t.Fatalf("initiative 3 under api moves behind %+v, want behind 5 naming 4, which only cli draws", step)
	}
}

func TestTUIWithNoTerminalRefusesAsAUsageError(t *testing.T) {
	unreachableAPI(t)
	root := NewRootCommand()
	root.SetArgs([]string{"tui"})
	err := root.Execute()
	if !errors.Is(err, goclikit.ErrUsage) {
		t.Fatalf("got %v, want a usage error", err)
	}
}

func TestPursuitGroupsDrawTheirMembersWithNoKeyRepeatedAndNoneHiddenByTheScope(t *testing.T) {
	read := sevenHighAndTwoLow()
	warehouse, security, neglected := api.PursuitSummary{Number: 100, Name: "Data Warehouse"}, api.PursuitSummary{Number: 101, Name: "Fleet Security"}, api.PursuitSummary{Number: 102, Name: "Neglected Way"}
	read.Initiatives[0].Pursuits = []api.PursuitSummary{warehouse, security}
	read.Initiatives[0].Issues = []api.BoardIssue{{Number: 50, Title: "Rotate the keys", State: "ready"}}
	read.Initiatives[6].Pursuits = []api.PursuitSummary{neglected}
	holding := func(pursuit *api.PursuitSummary, members ...int) api.BoardPursuitRollup {
		rollup := api.BoardPursuitRollup{Pursuit: pursuit}
		rollup.Members, rollup.InitiativeCount = members, len(members)
		return rollup
	}
	read.Pursuits = []api.BoardPursuitRollup{holding(&neglected, 7), holding(&warehouse, 1), holding(&security, 1), holding(nil, 2, 3, 4, 5, 6, 8, 9)}
	board := boardFromServer(t, read, boardScope{kind: scopePerTier, perTier: 5})
	frame := board.Frame(time.Now(), groupingPursuit)

	seen, under := map[string]bool{}, map[string]int{}
	var walk func(groups []gotermboard.Group, top string)
	walk = func(groups []gotermboard.Group, top string) {
		for _, group := range groups {
			if seen[group.Key] {
				t.Errorf("key %q is drawn twice, so the cursor would jump back to its first", group.Key)
			}
			seen[group.Key] = true
			if number, ok := numberFromKey(group.Key, initiativeKeyPrefix); ok && number == 1 {
				under[top]++
			}
			walk(group.Groups, cmp.Or(top, group.Key))
		}
	}
	walk(frame.Groups, "")
	if len(under) != 2 {
		t.Errorf("initiative 1 draws under %v, want under both of its pursuits", under)
	}
	at := slices.IndexFunc(frame.Groups, func(group gotermboard.Group) bool { return group.Key == groupingPursuit+":pursuit-102" })
	if at < 0 || frame.Groups[at].FilterOnly || !strings.Contains(frame.Groups[at].Title.Text(), "Neglected Way · 0 of 1") {
		t.Errorf("the pursuit whose only initiative is past the scope is hidden or mislabeled at group %d", at)
	}
}
