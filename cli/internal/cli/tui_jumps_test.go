package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/gotermboard"

	"github.com/datapointchris/slate/cli/internal/api"
)

// jumpAPI stands in for slate's API under every tab. At active it reads sevenHighAndTwoLow, with
// initiative 1 in pursuit 3010 and holding issue 50, whose live worklog 50.1 holds one session
// and links one pull request. Every status adds completed initiative 20, which holds the one
// document, its retro. Idea 4100 is in pursuit 3010, and idea 4000 in none. No focus is in force,
// and 3010 is the one pursuit. No worklog has expired or ended.
type jumpAPI struct {
	pullRequest string
	mu          sync.Mutex
	// statuses is what each board read asked for, empty for active.
	statuses []string
}

func (s *jumpAPI) serve(w http.ResponseWriter, r *http.Request) {
	garden := api.PursuitSummary{Number: 3010, Name: "Grow the garden"}
	day := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	var answer any
	switch r.URL.Path {
	case "/api/v1/initiatives/board":
		status := r.URL.Query().Get("status")
		s.mu.Lock()
		s.statuses = append(s.statuses, status)
		s.mu.Unlock()
		read := sevenHighAndTwoLow()
		read.Initiatives[0].Pursuits = []api.PursuitSummary{garden}
		read.Initiatives[0].Issues = []api.BoardIssue{{Number: 50, Title: "Rotate the keys", State: "ready"}}
		if status == everyInitiativeStatus {
			read.Initiatives = append(read.Initiatives, api.BoardInitiative{Number: 20, Name: "initiative 20", Status: "completed", Priority: 2})
			read.Tiers[0].InitiativeCount++
			read.Totals.InitiativeCount++
		}
		answer = read
	case "/api/v1/worklogs/50.2":
		ended := day.Add(-24 * time.Hour)
		answer = api.Worklog{
			Name: "50.2", RecordNumber: 50, Sequence: 2, StartedTS: ended.Add(-time.Hour), EndedTS: &ended, HeldUntil: &ended,
			Outcome: ptr("completed"), State: api.WorklogStateEnded, Stage: "landed",
			Record: api.RecordSummary{Number: 50, Kind: api.RecordKindIssue, Title: "Rotate the keys"},
		}
	case "/api/v1/worklogs":
		if state := r.URL.Query().Get("state"); state == api.WorklogStateExpired || state == api.WorklogStateEnded {
			answer = []api.Worklog{}
			break
		}
		answer = []api.Worklog{{
			Name: "50.1", RecordNumber: 50, Sequence: 1, StartedTS: day, State: api.WorklogStateLive, Stage: "pull_request",
			Record: api.RecordSummary{Number: 50, Kind: api.RecordKindIssue, Title: "Rotate the keys"},
			Spans:  []api.WorklogSpan{{Session: "5e179cd4-1340", StartedTS: day}},
			Links:  []api.WorklogLink{{Kind: api.WorklogLinkPullRequest, Ref: s.pullRequest, LinkedTS: day, LinkedBy: "someone"}},
		}}
	case "/api/v1/ideas":
		answer = []api.Idea{
			{Number: 4100, Title: "Compost the kitchen scraps", CreatedTS: day, UpdatedTS: day, InPursuits: api.InPursuits{Pursuits: []api.PursuitSummary{garden}}},
			{Number: 4000, Title: "A shelf for the library books", CreatedTS: day.AddDate(0, 0, -1), UpdatedTS: day},
		}
	case "/api/v1/initiatives/progress":
		answer = []any{}
	case "/api/v1/documents":
		w.Header().Set("X-Total-Records", "1")
		completed := api.RecordSummary{Number: 20, Kind: api.RecordKindInitiative, Title: "initiative 20", Status: ptr("completed")}
		answer = []api.ListedDocument{{Record: completed, DocumentSummary: api.DocumentSummary{
			RecordNumber: 20, Name: "retro", Address: "20/retro", Kind: "retro", CreatedTS: day,
			Latest: api.DocumentVersionSummary{Version: 1, WrittenBy: "someone", WrittenTS: day},
		}}}
	case "/api/v1/focus":
		answer = api.FocusStanding{Pursuits: []api.PursuitStanding{{PursuitFigures: api.PursuitFigures{Number: garden.Number}, Name: garden.Name, Status: "active"}}}
	case "/api/v1/pursuits":
		answer = []api.Pursuit{{Number: garden.Number, Name: garden.Name, Status: "active"}}
	case "/api/v1/focus/history":
		answer = []api.Focus{}
	case "/api/v1/focus/usage":
		answer = api.FocusUsage{}
	case "/api/v1/queue/board":
		first := &api.InitiativeSummary{Number: 1, Name: "initiative 1", Status: "active", Priority: 2}
		answer = api.QueueBoard{ReadTS: day, Claimed: []api.QueueBoardRow{}, Queue: []api.QueueBoardRow{{
			Kind: api.RecordKindIssue, Number: 50, Title: "Rotate the keys", Step: "build", Priority: 2, PlacedBy: "initiative",
			Initiative: first, Pursuits: []api.PursuitSummary{garden}, Slot: new(1),
		}}}
	case "/api/v1/records/50":
		answer = map[string]any{"kind": api.RecordKindIssue, "number": 50, "record": map[string]any{"number": 50, "title": "Rotate the keys"}}
	default:
		answer = map[string]any{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}

func (s *jumpAPI) lastStatus() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statuses[len(s.statuses)-1]
}

// openJumpTabs is slate tui's tabs on the stand-in, opening on view with every read landed, and
// with the cursor's row having read issue 50 once, as resting the cursor there would.
func openJumpTabs(t *testing.T, stand *jumpAPI, view string) *gotermboard.Tabs {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(stand.serve))
	t.Cleanup(server.Close)
	client := api.New(server.URL, server.Client())
	writer := newRankAndPinWriter(client)
	initiatives := newInitiativesBoard(client, writer, "", boardScope{kind: scopePerTier, perTier: defaultBoardPerTier}, "")
	initiatives.CursorOn(issueKey(50))
	initiatives.cursorRow.Refresh(context.Background())
	tabs, err := openTabs(tuiTabList(tabInputs{client: client, writer: writer, initiatives: func() *initiativesBoard { return initiatives }}), view)
	if err != nil {
		t.Fatal(err)
	}
	tabs.Update(tea.WindowSizeMsg{Width: 150, Height: 60})
	settle(tabs, tabs.Init())
	return tabs
}

// TestEachKindLandsWhereItsTabDrawsItWhole is the jump table: each row opens slate tui on a
// tab, jumps or presses keys, and reads the screen. A tab that declares a kind adds the rows that
// land on it. goTo is the jump that t makes from a row on another tab.
func TestEachKindLandsWhereItsTabDrawsItWhole(t *testing.T) {
	const webLink = "https://github.com/owner/repo/pull/40"
	initiative := func(number int) *gotermboard.EntityRef {
		ref := recordEntity(entityKindInitiative, number)
		return &ref
	}
	onIssue50 := recordEntity(entityKindIssue, 50)
	onEndedWorklog := gotermboard.EntityRef{Kind: entityKindWorklog, ID: "50.2"}
	for _, test := range []struct {
		name    string
		opensOn string
		goTo    *gotermboard.EntityRef
		// goToTab is the tab goTo jumps to, the initiatives where empty.
		goToTab string
		// keys are pressed in order. "show <tab>" presses tab until that tab shows, so a row reads
		// no tab order.
		keys []string
		// prsOnPath puts a prs on PATH, and pullRequest is the link issue 50's worklog holds.
		prsOnPath   bool
		pullRequest string
		// cursorOn is in the line under the cursor, and shows and hides each in the screen or not.
		cursorOn     string
		shows, hides []string
		// on is the tab showing once the keys are pressed, unchecked where empty.
		on string
		// handsOver says whether the last key handed the terminal to a program, unchecked where nil.
		handsOver *bool
		// readsAt is the status the newest board read asked for, empty for active.
		readsAt *string
	}{
		{
			name: "t on a pull request with a web link and prs on PATH opens prs", opensOn: tabInitiatives,
			goTo: &onIssue50, keys: []string{"enter", "j", "t", "1", "enter"}, prsOnPath: true, pullRequest: webLink, handsOver: new(true),
		},
		{
			name: "t on a pull request with no prs on PATH opens nothing", opensOn: tabInitiatives,
			goTo: &onIssue50, keys: []string{"enter", "j", "t", "1", "enter"}, pullRequest: webLink, handsOver: new(false),
		},
		{
			name: "t on a pull request that is no web link opens nothing", opensOn: tabInitiatives,
			goTo: &onIssue50, keys: []string{"enter", "j", "t", "1", "enter"}, prsOnPath: true, pullRequest: "ssh://host/owner/repo/pull/40",
			handsOver: new(false),
		},
		{
			name: "t on a pull request asks between prs and its worklog's row", opensOn: tabInitiatives,
			goTo: &onIssue50, keys: []string{"enter", "j", "t"}, pullRequest: webLink, shows: []string{"worklogs  worklog 50.1"},
		},
		{
			name: "t on an issue's session lands on its worklog's row", opensOn: tabInitiatives,
			goTo: &onIssue50, keys: []string{"enter", "j", "j", "t"}, pullRequest: webLink, cursorOn: "50.1", on: tabWorklogs,
		},
		{
			name: "t to a worklog that ended past the bounded reads reads it by name and lands on its row", opensOn: tabInitiatives,
			goTo: &onEndedWorklog, goToTab: tabWorklogs, pullRequest: webLink, cursorOn: "50.2", on: tabWorklogs,
		},
		{
			name: "t from a worklog lands on its record", opensOn: tabWorklogs,
			keys: []string{"t", "1", "enter"}, pullRequest: webLink, cursorOn: "Rotate the keys", on: tabInitiatives,
		},
		{
			name: "esc from the record comes back to the worklog", opensOn: tabWorklogs,
			keys: []string{"t", "1", "enter", "esc"}, pullRequest: webLink, cursorOn: "50.1", on: tabWorklogs,
		},
		{
			name: "* on a pursuit narrows the ideas tab to the ideas in it", opensOn: tabIdeas,
			keys: []string{"*", "2", "enter"}, pullRequest: webLink,
			shows: []string{"Compost the kitchen scraps"}, hides: []string{"A shelf for the library books"},
		},
		{
			name: "* on a pursuit narrows the initiatives tab to the initiatives serving it", opensOn: tabIdeas,
			keys: []string{"*", "2", "enter", "show " + tabInitiatives}, pullRequest: webLink,
			shows: []string{"initiative 1"}, hides: []string{"initiative 2", "initiative 8"},
		},
		{
			name: "a jump to the sixth initiative of a tier lands on its row", opensOn: tabIdeas,
			goTo: initiative(6), pullRequest: webLink, cursorOn: "initiative 6", readsAt: new(""),
		},
		{
			name: "a jump to a completed initiative reads every status and lands on its row", opensOn: tabIdeas,
			goTo: initiative(20), pullRequest: webLink, cursorOn: "initiative 20", readsAt: new(everyInitiativeStatus),
		},
		{
			name: "esc from the completed initiative reads the status the tab held", opensOn: tabIdeas,
			goTo: initiative(20), keys: []string{"esc", "show " + tabInitiatives}, pullRequest: webLink,
			hides: []string{"initiative 20"}, readsAt: new(""),
		},
		{
			name: "t on a document of a completed initiative reads every status and lands on its row", opensOn: tabDocuments,
			keys: []string{"t"}, pullRequest: webLink, cursorOn: "initiative 20", readsAt: new(everyInitiativeStatus),
		},
		{
			name: "esc from a document's initiative comes back to the document", opensOn: tabDocuments,
			keys: []string{"t", "esc"}, pullRequest: webLink, cursorOn: "retro",
		},
		{
			name: "t on an initiative lands on its pursuit's row", opensOn: tabIdeas,
			goTo: initiative(1), keys: []string{"t"}, pullRequest: webLink, cursorOn: "Grow the garden", on: tabPursuits,
		},
		{
			name: "esc from the pursuit comes back to the initiative", opensOn: tabIdeas,
			goTo: initiative(1), keys: []string{"t", "esc"}, pullRequest: webLink, cursorOn: "initiative 1",
		},
		{
			name: "t on a queue row lands on its record", opensOn: tabQueue,
			keys: []string{"t", "1", "enter"}, pullRequest: webLink, cursorOn: "Rotate the keys", on: tabInitiatives, shows: []string{"slate · initiatives"},
		},
		{
			name: "t on a queue row lands on its initiative", opensOn: tabQueue,
			keys: []string{"t", "2", "enter"}, pullRequest: webLink, cursorOn: "initiative 1", on: tabInitiatives, shows: []string{"slate · initiatives"},
		},
		{
			name: "t on a queue row lands on its pursuit", opensOn: tabQueue,
			keys: []string{"t", "3", "enter"}, pullRequest: webLink, cursorOn: "Grow the garden", on: tabPursuits, shows: []string{"slate · pursuits"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := t.TempDir()
			if test.prsOnPath {
				if err := os.WriteFile(filepath.Join(path, "prs"), []byte("#!/bin/sh\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", path)
			stand := &jumpAPI{pullRequest: test.pullRequest}
			tabs := openJumpTabs(t, stand, test.opensOn)
			if test.goTo != nil {
				settle(tabs, gotermboard.GoTo(cmp.Or(test.goToTab, tabInitiatives), *test.goTo))
			}
			var last tea.Cmd
			for _, key := range test.keys {
				if name, ok := strings.CutPrefix(key, "show "); ok {
					showTab(tabs, name)
					continue
				}
				last = pressKey(tabs, key)
				settle(tabs, last)
			}
			screen := tabs.View().Content
			if test.cursorOn != "" && !strings.Contains(cursorLine(screen), test.cursorOn) {
				t.Errorf("the cursor is on %q, want the line naming %q:\n%s", cursorLine(screen), test.cursorOn, screen)
			}
			if test.on != "" && tabs.ReopensOn() != test.on {
				t.Errorf("the %s tab shows, want %s:\n%s", tabs.ReopensOn(), test.on, screen)
			}
			for _, want := range test.shows {
				if !strings.Contains(screen, want) {
					t.Errorf("the screen should show %q:\n%s", want, screen)
				}
			}
			for _, unwanted := range test.hides {
				if strings.Contains(screen, unwanted) {
					t.Errorf("the screen should not show %q:\n%s", unwanted, screen)
				}
			}
			if test.handsOver != nil && *test.handsOver != (last != nil) {
				t.Errorf("the last key handed the terminal over: %t, want %t:\n%s", last != nil, *test.handsOver, screen)
			}
			if test.readsAt != nil && stand.lastStatus() != *test.readsAt {
				t.Errorf("the board read last at status %q, want %q, after reading at %q", stand.lastStatus(), *test.readsAt, stand.statuses)
			}
		})
	}
}

// cursorLine is the line the cursor's mark leads.
func cursorLine(screen string) string {
	for line := range strings.SplitSeq(screen, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "▌") {
			return line
		}
	}
	return ""
}

// showTab presses tab until the tab named name shows.
func showTab(tabs *gotermboard.Tabs, name string) {
	for range tuiTabNames() {
		if tabs.ReopensOn() == name {
			return
		}
		settle(tabs, pressKey(tabs, "tab"))
	}
}

func pressKey(tabs *gotermboard.Tabs, key string) tea.Cmd {
	msg := tea.KeyPressMsg{Code: []rune(key)[0], Text: key}
	switch key {
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		msg = tea.KeyPressMsg{Code: tea.KeyTab}
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	_, cmd := tabs.Update(msg)
	return cmd
}

// settle runs cmd and what the messages it yields start, a few rounds deep, feeding each message to
// model. Every read here is local, so a command still waiting after 200ms is a tick or a spinner,
// and is dropped.
func settle(model tea.Model, cmd tea.Cmd) {
	pending := []tea.Cmd{cmd}
	for range 4 {
		var msgs []tea.Msg
		for len(pending) > 0 {
			results := make(chan tea.Msg, len(pending))
			for _, c := range pending {
				if c == nil {
					results <- nil
					continue
				}
				go func() { results <- c() }()
			}
			waiting, deadline := len(pending), time.After(200*time.Millisecond)
			pending = nil
		collect:
			for range waiting {
				select {
				case msg := <-results:
					if batch, ok := msg.(tea.BatchMsg); ok {
						pending = append(pending, batch...)
					} else if msg != nil {
						msgs = append(msgs, msg)
					}
				case <-deadline:
					break collect
				}
			}
		}
		if len(msgs) == 0 {
			return
		}
		for _, msg := range msgs {
			_, next := model.Update(msg)
			pending = append(pending, next)
		}
	}
}
