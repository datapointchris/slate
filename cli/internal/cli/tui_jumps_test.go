package cli

import (
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
// initiative 1 in pursuit 3010 and holding issue 50, whose worklog links one pull request. Every
// status adds completed initiative 20. Idea 4100 is in pursuit 3010, and idea 4000 in none.
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
	case "/api/v1/worklogs":
		answer = []api.Worklog{{Name: "50.1", RecordNumber: 50, Sequence: 1, StartedTS: day, Links: []api.WorklogLink{
			{Kind: api.WorklogLinkPullRequest, Ref: s.pullRequest, LinkedTS: day, LinkedBy: "someone"},
		}}}
	case "/api/v1/ideas":
		answer = []api.Idea{
			{Number: 4100, Title: "Compost the kitchen scraps", CreatedTS: day, UpdatedTS: day, InPursuits: api.InPursuits{Pursuits: []api.PursuitSummary{garden}}},
			{Number: 4000, Title: "A shelf for the library books", CreatedTS: day.AddDate(0, 0, -1), UpdatedTS: day},
		}
	case "/api/v1/initiatives/progress":
		answer = []any{}
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
	initiatives := newInitiativesBoard(client, "", boardScope{kind: scopePerTier, perTier: defaultBoardPerTier}, "")
	initiatives.CursorOn(issueKey(50))
	initiatives.cursorRow.Refresh(context.Background())
	tabs, err := openTabs(tuiTabList(tabInputs{client: client, initiatives: func() *initiativesBoard { return initiatives }}), view)
	if err != nil {
		t.Fatal(err)
	}
	tabs.Update(tea.WindowSizeMsg{Width: 150, Height: 60})
	settle(tabs, tabs.Init())
	return tabs
}

// TestEachKindLandsWhereItsTabDrawsItWhole is the jump table: each row opens slate tui on a
// tab, jumps or presses keys, and reads the screen. A tab that declares a kind adds the rows that
// land on it. goTo stands in for t on a row of a tab that is not built yet.
func TestEachKindLandsWhereItsTabDrawsItWhole(t *testing.T) {
	const webLink = "https://github.com/owner/repo/pull/40"
	initiative := func(number int) *gotermboard.EntityRef {
		ref := recordEntity(entityKindInitiative, number)
		return &ref
	}
	onIssue50 := recordEntity(entityKindIssue, 50)
	for _, test := range []struct {
		name    string
		opensOn string
		goTo    *gotermboard.EntityRef
		keys    []string
		// prsOnPath puts a prs on PATH, and pullRequest is the link issue 50's worklog holds.
		prsOnPath   bool
		pullRequest string
		// cursorOn is in the line under the cursor, and shows and hides each in the screen or not.
		cursorOn     string
		shows, hides []string
		// handsOver says the last key handed the terminal to a program.
		handsOver bool
		// readsAt is the status the newest board read asked for, empty for active.
		readsAt *string
	}{
		{
			name: "t on a pull request with a web link and prs on PATH opens prs", opensOn: tabInitiatives,
			goTo: &onIssue50, keys: []string{"enter", "j", "t"}, prsOnPath: true, pullRequest: webLink, handsOver: true,
		},
		{
			name: "t on a pull request with no prs on PATH says so", opensOn: tabInitiatives,
			goTo: &onIssue50, keys: []string{"enter", "j", "t"}, pullRequest: webLink,
			shows: []string{"prs is not on PATH, so o opens owner/repo#40 in the browser"},
		},
		{
			name: "t on a pull request that is no web link says so", opensOn: tabInitiatives,
			goTo: &onIssue50, keys: []string{"enter", "j", "t"}, prsOnPath: true, pullRequest: "ssh://host/owner/repo/pull/40",
			shows: []string{"ssh://host/owner/repo/pull/40 is not a web link, so nothing opened it"},
		},
		{
			name: "* on a pursuit narrows the ideas tab to the ideas in it", opensOn: tabIdeas,
			keys: []string{"*", "2", "enter"}, pullRequest: webLink,
			shows: []string{"Compost the kitchen scraps"}, hides: []string{"A shelf for the library books"},
		},
		{
			name: "* on a pursuit narrows the initiatives tab to the initiatives serving it", opensOn: tabIdeas,
			keys: []string{"*", "2", "enter", "tab"}, pullRequest: webLink,
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
			goTo: initiative(20), keys: []string{"esc", "tab"}, pullRequest: webLink,
			hides: []string{"initiative 20"}, readsAt: new(""),
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
				settle(tabs, gotermboard.GoTo(tabInitiatives, *test.goTo))
			}
			var last tea.Cmd
			for _, key := range test.keys {
				last = pressKey(tabs, key)
				settle(tabs, last)
			}
			screen := tabs.View().Content
			if test.cursorOn != "" && !strings.Contains(cursorLine(screen), test.cursorOn) {
				t.Errorf("the cursor is on %q, want the line naming %q:\n%s", cursorLine(screen), test.cursorOn, screen)
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
			if test.handsOver && last == nil {
				t.Errorf("the last key should hand the terminal over:\n%s", screen)
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
