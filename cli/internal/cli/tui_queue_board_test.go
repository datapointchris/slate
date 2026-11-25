package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/gotermboard"
	"github.com/datapointchris/gotermstyle"

	"github.com/datapointchris/slate/cli/internal/api"
)

// queueStandIn stands in for slate's queue board, focus, record and rank routes, and the
// initiatives board. The queue holds pinned initiative 3148, high initiative 1482 with its issues
// 1192 and 1193 inside it, high initiative 1963 and high idea 4100, then 54 ideas at no priority,
// 5050 among them past the first 50. Initiative 4271 is claimed and waits on a read of its plan.
type queueStandIn struct {
	mu         sync.Mutex
	boardReads int
	writes     []string
}

const (
	queueStandInHolder = "claude-code/cda42040-8d12-4c30-9d5f-f1d9325ef5af"
	queueStandInPast   = 5050
)

func queueStandInBoard() api.QueueBoard {
	safe := api.PursuitSummary{Number: 2990, Name: "Keep the garden safe"}
	gates := &api.InitiativeSummary{Number: 1482, Name: "Lock every gate in the garden", Status: "active", Priority: 2}
	slot := func(number int) *int { return &number }
	queue := []api.QueueBoardRow{
		{Kind: "initiative", Number: 3148, Title: "Every change to the watering schedule runs beside the old one first", Step: "shape", Priority: 4, OwnPriority: 4, Pinned: true, PlacedBy: "pin", Slot: slot(3148)},
		{Kind: "initiative", Number: 1482, Title: gates.Name, Step: "design", Priority: 2, OwnPriority: 2, PlacedBy: "own", Repos: []string{"gates", "shed", "fence"}, Pursuits: []api.PursuitSummary{safe}, OpenCount: new(2), Slot: slot(1482)},
		{Kind: "issue", Number: 1192, Title: "Decide whether the side gate takes a key or a code", Step: "decide", Priority: 2, PlacedBy: "initiative", Initiative: gates, Repos: []string{"gates"}, Pursuits: []api.PursuitSummary{safe}, Slot: slot(1482)},
		{Kind: "issue", Number: 1193, Title: "Fit the padlock on the back gate", Step: "build", Priority: 2, PlacedBy: "initiative", Initiative: gates, Repos: []string{"gates"}, Pursuits: []api.PursuitSummary{safe}, Slot: slot(1482)},
		{Kind: "initiative", Number: 1963, Title: "Sort the shed into labeled bins", Step: "design", Priority: 2, OwnPriority: 2, PlacedBy: "own", Repos: []string{"shed"}, Slot: slot(1963)},
		{Kind: "idea", Number: 4100, Title: "Compost the kitchen scraps", Step: "discuss", Priority: 2, OwnPriority: 2, PlacedBy: "own", Slot: slot(4100)},
	}
	for number := 5001; number <= 5054; number++ {
		title := "An idea for later " + strconv.Itoa(number)
		if number == queueStandInPast {
			title = "Paint the fence"
		}
		queue = append(queue, api.QueueBoardRow{Kind: "idea", Number: number, Title: title, Step: "discuss", PlacedBy: "own", Slot: slot(number)})
	}
	waited := time.Now().Add(-time.Hour)
	claimed := []api.QueueBoardRow{{
		Kind: "initiative", Number: 4271, Title: "The garden keeps its own journal", Step: "preflight", Priority: 2, OwnPriority: 2, PlacedBy: "own",
		Holder: new(queueStandInHolder), ExpiresTS: new(waited.Add(24 * time.Hour)), Waiting: &api.ClaimWait{WaitingOn: "the user reading its work plan", StartedTS: waited},
		Repos: []string{"journal"}, Slot: slot(4271),
	}}
	return api.QueueBoard{ReadTS: time.Now(), Queue: queue, Claimed: claimed}
}

func (s *queueStandIn) serve(w http.ResponseWriter, r *http.Request) {
	var answer any
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost:
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.writes = append(s.writes, path+" "+strings.TrimSpace(string(body)))
		s.mu.Unlock()
		answer = map[string]any{"side_effects": []any{}, "warnings": []any{}}
	case path == "/api/v1/queue/board":
		s.mu.Lock()
		s.boardReads++
		s.mu.Unlock()
		answer = queueStandInBoard()
	case path == "/api/v1/focus":
		answer = api.FocusStanding{Pursuits: []api.PursuitStanding{{PursuitFigures: api.PursuitFigures{Number: 2990, Factor: new(0.88)}, Name: "Keep the garden safe", Status: "active"}}}
	case strings.HasPrefix(path, "/api/v1/records/"):
		number, _ := strconv.Atoi(strings.TrimPrefix(path, "/api/v1/records/"))
		kind := api.RecordKindIdea
		for _, row := range queueStandInBoard().Queue {
			if row.Number == number {
				kind = row.Kind
			}
		}
		answer = map[string]any{"kind": kind, "number": number, "record": map[string]any{
			"number": number, "rank": 183, "pinned_ts": nil, "description": fmt.Sprintf("The whole of record %d.", number),
		}}
	case path == "/api/v1/initiatives/board":
		answer = sevenHighAndTwoLow()
	case path == "/api/v1/initiatives/progress":
		answer = []any{}
	default:
		answer = map[string]any{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}

func (s *queueStandIn) counts() (boardReads int, writes []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.boardReads, append([]string(nil), s.writes...)
}

// press sends key to model, and runs and feeds back what it starts.
func press(model *gotermboard.Model, key string) {
	msg := tea.KeyPressMsg{Code: []rune(key)[0], Text: key}
	switch key {
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	_, cmd := model.Update(msg)
	settle(model, cmd)
}

func TestTheQueueTabDrawsTheClaimedApartSaysWhyEachRowSitsThereAndMovesByTheAPIsRules(t *testing.T) {
	stand := &queueStandIn{}
	server := httptest.NewServer(http.HandlerFunc(stand.serve))
	t.Cleanup(server.Close)
	client := api.New(server.URL, server.Client())
	writer := newRankAndPinWriter(client)
	board := newQueueBoard(client, writer)
	model := gotermboard.NewModel(board, gotermstyle.NewPalette(false))
	model.Update(tea.WindowSizeMsg{Width: 220, Height: 80})
	settle(model, model.Init())
	ctx := context.Background()
	atRecord := func(number int) string {
		t.Helper()
		if !model.MoveCursorTo(queueRowKey(number)) {
			t.Fatalf("no line holds %d:\n%s", number, model.View().Content)
		}
		model.Update(tea.WindowSizeMsg{Width: 220, Height: 80})
		board.records.Refresh(ctx)
		model.Rebuild()
		return strings.Join(strings.Fields(panelOf(model.View().Content)), " ")
	}

	screen := model.View().Content
	if strip := strings.SplitN(screen, "\n", 2)[0]; !strings.Contains(strip, "60 open · 1 claimed · first 50") {
		t.Errorf("the strip should count every row and name the scope: %s", strip)
	}
	if at := linesAt(tableOf(screen), []string{"3148", "1482", "1192", "1193", "claimed · 1", "4271"}); !isIncreasing(at) {
		t.Errorf("the queue should draw in its order with the claimed apart after it, at lines %v:\n%s", at, screen)
	}
	if line := linesAt(screen, []string{"4271"})[0]; line < 0 || !strings.Contains(strings.Split(screen, "\n")[line], "held by cda42040 · wait") {
		t.Errorf("the claimed row should name its holder and what its claim waits on:\n%s", screen)
	}
	if panel := atRecord(4271); !strings.Contains(panel, "waits on the user reading its work plan") {
		t.Errorf("4271's panel should say what its claim waits on:\n%s", panel)
	}

	panel := atRecord(1192)
	for _, want := range []string{
		"1192 · issue · decide · high placed by initiative 1482 Lock every gate in the garden · its own priority is none pin none rank 183",
		"standing Keep the garden safe factor 0.88", "The whole of record 1192.",
	} {
		if !strings.Contains(panel, want) {
			t.Errorf("1192's panel should hold %q:\n%s", want, panel)
		}
	}

	reads, _ := stand.counts()
	for _, scope := range []struct{ key, want string }{{"2", "first 200"}, {"3", "all"}, {"1", "first 50"}} {
		key, want := scope.key, scope.want
		press(model, key)
		screen = model.View().Content
		if strip := strings.SplitN(screen, "\n", 2)[0]; !strings.Contains(strip, want) {
			t.Errorf("%s should show the %s: %s", key, want, strip)
		}
		if shows := strings.Contains(screen, "Paint the fence"); shows != (want != "first 50") {
			t.Errorf("under the %s, the 55th row shows: %t", want, shows)
		}
	}
	if after, _ := stand.counts(); after != reads {
		t.Errorf("1, 2 and 3 should cut one whole read, and read the board %d times more", after-reads)
	}

	atRecord(1192)
	press(model, "<")
	if screen = model.View().Content; !strings.Contains(screen, "1192 sits inside initiative 1482 Lock every gate in the garden, so it moves only among that initiative's issues, and none sits above it") {
		t.Errorf("< on an initiative's first issue should say it moves only among that initiative's issues:\n%s", screen)
	}

	atRecord(4100)
	press(model, "<")
	if _, writes := stand.counts(); len(writes) != 1 || writes[0] != `/api/v1/ideas/4100/rank {"before":"1963"}` {
		t.Errorf("< on idea 4100 should rank it ahead of 1963 through the idea's rank, wrote %q", writes)
	}

	for _, key := range strings.Split("/Paint", "") {
		press(model, key)
	}
	press(model, "enter")
	atRecord(queueStandInPast)
	press(model, "<")
	if screen = model.View().Content; !strings.Contains(screen, "5049 An idea for later 5049 is hidden by the scope") {
		t.Errorf("< past a row hidden by the scope should ask first, naming it:\n%s", screen)
	}
	press(model, "esc")
	press(model, "esc")
	if _, writes := stand.counts(); len(writes) != 1 {
		t.Errorf("the move past a hidden row should write nothing until answered, wrote %q", writes)
	}

	initiatives := newInitiativesBoard(client, writer, "", boardScope{kind: scopeEveryInitiative}, "")
	initiativesModel := gotermboard.NewModel(initiatives, gotermstyle.NewPalette(false))
	initiativesModel.Update(tea.WindowSizeMsg{Width: 220, Height: 80})
	settle(initiativesModel, initiativesModel.Init())
	initiativesModel.MoveCursorTo(initiativeKey(1))
	if _, cmd := initiativesModel.Update(tea.KeyPressMsg{Code: 'P', Text: "P"}); cmd == nil {
		t.Fatalf("P on initiative 1 should start its pin:\n%s", initiativesModel.View().Content)
	}
	atRecord(1193)
	press(model, "<")
	if screen = model.View().Content; !strings.Contains(screen, "pinning 1 is still under way, so nothing else was written") {
		t.Errorf("a move on the queue tab should wait on the pin the initiatives tab started:\n%s", screen)
	}

	asService(t, serviceIDP(t).URL, server.URL, serviceSecret)
	out, err := runService(t, "tui", "--view", tabQueue, "--json")
	if err != nil {
		t.Fatal(err)
	}
	validateAgainst(t, "tui-queue.schema.json", out)
	var printed queueSnapshot
	if err := json.Unmarshal([]byte(out), &printed); err != nil {
		t.Fatal(err)
	}
	if len(printed.Board.Queue) != 60 || len(printed.Board.Claimed) != 1 || printed.CursorRecord == nil || printed.CursorRecord.Number != 3148 {
		t.Errorf("--json should print every row and the first row's record:\n%s", out)
	}
}
