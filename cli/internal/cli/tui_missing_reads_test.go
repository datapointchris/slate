package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/datapointchris/slate/cli/internal/api"
)

// TestATabNamesTheReadItsAPILacks is the missing-read table: each row opens a tab on an API
// older than this CLI. --once exits on the error naming the read it lacks, and the live tab
// draws that error below the strip, where the rows or the chart would be. A tab reading a
// route that an older API lacks adds its row.
func TestATabNamesTheReadItsAPILacks(t *testing.T) {
	for _, test := range []struct {
		name, view string
		serve      http.HandlerFunc
		lacks      error
	}{
		{
			name: "documents listed with no record", view: tabDocuments,
			serve: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/documents" {
					http.NotFound(w, r)
					return
				}
				day := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode([]api.DocumentSummary{{
					RecordNumber: 4271, Name: "design", Address: "4271/design", Kind: "design", CreatedTS: day,
					Latest: api.DocumentVersionSummary{Version: 1, WrittenBy: "someone", WrittenTS: day},
				}})
			},
			lacks: errDocumentsLackRecord,
		},
		{
			name: "the usage by day", view: tabPursuits,
			serve: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/focus":
					_ = json.NewEncoder(w).Encode(api.FocusStanding{})
				case "/api/v1/pursuits", "/api/v1/focus/history":
					_, _ = w.Write([]byte(`[]`))
				default:
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"error":"Not Found","message":"no route answers ` + r.URL.Path + `"}`))
				}
			},
			lacks: errFocusUsageUnanswered,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.serve)
			t.Cleanup(server.Close)

			asService(t, serviceIDP(t).URL, server.URL, serviceSecret)
			if _, err := runService(t, "tui", "--view", test.view, "--once"); !errors.Is(err, test.lacks) {
				t.Errorf("--once should exit on %q, exited on %v", test.lacks, err)
			}

			client := api.New(server.URL, server.Client())
			in := tabInputs{client: client, initiatives: func() *initiativesBoard {
				return newInitiativesBoard(client, "", boardScope{kind: scopePerTier, perTier: defaultBoardPerTier}, "")
			}}
			tabs, err := openTabs(tuiTabList(in), test.view)
			if err != nil {
				t.Fatal(err)
			}
			tabs.Update(tea.WindowSizeMsg{Width: 150, Height: 40})
			settle(tabs, tabs.Init())
			screen := tabs.View().Content
			if _, below, _ := strings.Cut(screen, "\n"); !strings.Contains(below, test.lacks.Error()) {
				t.Errorf("below the strip, the tab should draw %q:\n%s", test.lacks, screen)
			}
		})
	}
}
