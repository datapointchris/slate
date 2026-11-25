package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
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

// queueStandIn stands in for slate's queue board, focus, record, rank and pin routes, and the
// initiatives board. The queue holds pinned initiative 3148, high initiative 1482 with its issues
// 1192 and 1193 inside it, high initiative 1963 and high idea 4100, then 54 ideas at no priority,
// 5050 among them past the first 50. Initiative 4271 is claimed and waits on a read of its plan.
// Each queued row carries the moves the API would answer for it, and a pin written to a record
// shows on its row and its read.
type queueStandIn struct {
	mu         sync.Mutex
	boardReads int
	writes     []string
	pinned     map[int]bool
}

const (
	queueStandInHolder = "claude-code/cda42040-8d12-4c30-9d5f-f1d9325ef5af"
	queueStandInPast   = 5050
	// queueStandInInside is the refusal the API answers for 1192 moved ahead.
	queueStandInInside = "#1192 sits inside initiative #1482 'Lock every gate in the garden', so it moves only among that initiative's issues."
)

func queueStandInBoard(pinned map[int]bool) api.QueueBoard {
	safe := api.PursuitSummary{Number: 2990, Name: "Keep the garden safe"}
	gates := &api.InitiativeSummary{Number: 1482, Name: "Lock every gate in the garden", Status: "active", Priority: 2}
	slot := func(number int) *int { return &number }
	beside := func(number int, passes ...int) api.QueueMove { return api.QueueMove{Beside: &number, Passes: passes} }
	refused := func(why string) api.QueueMove { return api.QueueMove{Passes: []int{}, Refused: why} }
	moves := func(ahead, behind api.QueueMove) *api.QueueRowMoves {
		return &api.QueueRowMoves{Ahead: ahead, Behind: behind}
	}
	acrossThePin := refused("#3148 is pinned and #1482 is not, so a move cannot place one beside the other.")
	acrossAPriority := refused("A move orders records only within one priority.")
	queue := []api.QueueBoardRow{
		{Kind: "initiative", Number: 3148, Title: "Every change to the watering schedule runs beside the old one first", Step: "shape", Priority: 4, OwnPriority: 4, PlacedBy: "own", Slot: slot(3148), Moves: moves(refused("#3148 is first in the queue."), acrossThePin)},
		{Kind: "initiative", Number: 1482, Title: gates.Name, Step: "design", Priority: 2, OwnPriority: 2, PlacedBy: "own", Repos: []string{"gates", "shed", "fence"}, Pursuits: []api.PursuitSummary{safe}, OpenCount: new(2), Slot: slot(1482), Moves: moves(acrossThePin, beside(1963, 1963))},
		{Kind: "issue", Number: 1192, Title: "Decide whether the side gate takes a key or a code", Step: "decide", Priority: 2, PlacedBy: "initiative", Initiative: gates, Repos: []string{"gates"}, Pursuits: []api.PursuitSummary{safe}, Slot: slot(1482), Moves: moves(refused(queueStandInInside), beside(1193, 1193))},
		{Kind: "issue", Number: 1193, Title: "Fit the padlock on the back gate", Step: "build", Priority: 2, PlacedBy: "initiative", Initiative: gates, Repos: []string{"gates"}, Pursuits: []api.PursuitSummary{safe}, Slot: slot(1482), Moves: moves(beside(1192, 1192), refused("#1193 moves only among initiative #1482's issues."))},
		{Kind: "initiative", Number: 1963, Title: "Sort the shed into labeled bins", Step: "design", Priority: 2, OwnPriority: 2, PlacedBy: "own", Repos: []string{"shed"}, Slot: slot(1963), Moves: moves(beside(1482, 1193, 1192, 1482), beside(4100, 4100))},
		{Kind: "idea", Number: 4100, Title: "Compost the kitchen scraps", Step: "discuss", Priority: 2, OwnPriority: 2, PlacedBy: "own", Slot: slot(4100), Moves: moves(beside(1963, 1963), acrossAPriority)},
	}
	for number := 5001; number <= 5054; number++ {
		title := "An idea for later " + strconv.Itoa(number)
		if number == queueStandInPast {
			title = "Paint the fence"
		}
		ahead, behind := beside(number-1, number-1), beside(number+1, number+1)
		switch number {
		case 5001:
			ahead = acrossAPriority
		case 5054:
			behind = refused("#5054 is last in the queue.")
		}
		queue = append(queue, api.QueueBoardRow{Kind: "idea", Number: number, Title: title, Step: "discuss", PlacedBy: "own", Slot: slot(number), Moves: moves(ahead, behind)})
	}
	for at := range queue {
		if pinned[queue[at].Number] {
			queue[at].Pinned, queue[at].PlacedBy = true, api.PlacedByPin
		}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.Method == http.MethodPost || r.Method == http.MethodDelete:
		body, _ := io.ReadAll(r.Body)
		write := strings.TrimSpace(path + " " + strings.TrimSpace(string(body)))
		if r.Method == http.MethodDelete {
			write = "DELETE " + write
		}
		s.writes = append(s.writes, write)
		if pinned, isPin := strings.CutSuffix(path, "/pin"); isPin {
			number, _ := strconv.Atoi(pinned[strings.LastIndex(pinned, "/")+1:])
			s.pinned[number] = r.Method == http.MethodPost
		}
		answer = map[string]any{"side_effects": []any{}, "warnings": []any{}}
	case path == "/api/v1/queue/board":
		s.boardReads++
		answer = queueStandInBoard(s.pinned)
	case path == "/api/v1/focus":
		answer = api.FocusStanding{Pursuits: []api.PursuitStanding{{PursuitFigures: api.PursuitFigures{Number: 2990, Factor: new(0.88)}, Name: "Keep the garden safe", Status: "active"}}}
	case strings.HasPrefix(path, "/api/v1/records/"):
		number, _ := strconv.Atoi(strings.TrimPrefix(path, "/api/v1/records/"))
		kind := api.RecordKindIdea
		for _, row := range queueStandInBoard(s.pinned).Queue {
			if row.Number == number {
				kind = row.Kind
			}
		}
		var pinnedTS *time.Time
		if s.pinned[number] {
			pinnedTS = new(time.Now().Add(-time.Hour))
		}
		answer = map[string]any{"kind": kind, "number": number, "record": map[string]any{
			"number": number, "rank": 183, "pinned_ts": pinnedTS, "description": fmt.Sprintf("The whole of record %d.", number),
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

func TestTheQueueTabDrawsTheClaimedApartSaysWhyEachRowSitsThereAndMovesWhereTheBoardSays(t *testing.T) {
	stand := &queueStandIn{pinned: map[int]bool{3148: true}}
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
	if _, writes := stand.counts(); len(writes) != 0 {
		t.Errorf("< where the board answers a refusal should write nothing, wrote %q", writes)
	}
	if screen = model.View().Content; !strings.Contains(screen, queueStandInInside) {
		t.Errorf("the footer should say why the API refuses the move:\n%s", screen)
	}
	press(model, ">")
	atRecord(4100)
	press(model, "<")
	if _, writes := stand.counts(); !slices.Equal(writes, []string{`/api/v1/issues/1192/rank {"after":"1193"}`, `/api/v1/ideas/4100/rank {"before":"1963"}`}) {
		t.Errorf("> on 1192 and < on 4100 should each rank the record beside the one the board names, wrote %q", writes)
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
	if _, writes := stand.counts(); len(writes) != 2 {
		t.Errorf("the move past a hidden row should write nothing until answered, wrote %q", writes)
	}

	// P on 4100 pins it, and the cursor leaves before the pin lands. Back on 4100, P waits for a
	// read of 4100 newer than the pin, so it unpins rather than pinning again.
	atRecord(4100)
	_, pinning := model.Update(tea.KeyPressMsg{Code: 'P', Text: "P"})
	atRecord(1963)
	settle(model, pinning)
	board.board.Refresh(ctx)
	board.records.Refresh(ctx)
	model.Rebuild()
	model.MoveCursorTo(queueRowKey(4100))
	model.Update(tea.WindowSizeMsg{Width: 220, Height: 80})
	press(model, "P")
	if _, writes := stand.counts(); len(writes) != 3 {
		t.Errorf("P back on 4100 should write nothing until 4100 is read again, wrote %q", writes)
	}
	board.records.Refresh(ctx)
	model.Rebuild()
	press(model, "P")
	if _, writes := stand.counts(); len(writes) != 4 || writes[3] != "DELETE /api/v1/ideas/4100/pin" {
		t.Errorf("P on 4100 once read again should unpin it, wrote %q", writes)
	}

	initiatives := newInitiativesBoard(client, writer, "", boardScope{kind: scopeEveryInitiative}, "")
	initiativesModel := gotermboard.NewModel(initiatives, gotermstyle.NewPalette(false))
	initiativesModel.Update(tea.WindowSizeMsg{Width: 220, Height: 80})
	settle(initiativesModel, initiativesModel.Init())
	initiativesModel.MoveCursorTo(initiativeKey(1))
	if _, cmd := initiativesModel.Update(tea.KeyPressMsg{Code: 'P', Text: "P"}); cmd == nil {
		t.Fatalf("P on initiative 1 should start its pin:\n%s", initiativesModel.View().Content)
	}
	atRecord(1192)
	press(model, ">")
	if _, writes := stand.counts(); len(writes) != 4 {
		t.Errorf("a move on the queue tab should wait on the pin the initiatives tab started, wrote %q", writes)
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
	text, err := os.ReadFile("tui-queue.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Definitions map[string]struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(text, &schema); err != nil {
		t.Fatal(err)
	}
	if names := schema.Definitions["row"].Properties["placed_by"].Enum; !slices.Equal(names, api.PlacedByNames) {
		t.Errorf("the schema's placed_by = %v, the API's are %v", names, api.PlacedByNames)
	}
}
