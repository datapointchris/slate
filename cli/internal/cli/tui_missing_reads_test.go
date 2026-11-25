package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/datapointchris/slate/cli/internal/api"
)

// TestATabNamesTheReadItsAPILacks is the missing-read table: each row opens a tab on an API
// older than this CLI, and reads the line below the strip that names the read it lacks, where
// the rows or the chart would be. A tab reading a route that an older API lacks adds its row.
func TestATabNamesTheReadItsAPILacks(t *testing.T) {
	for _, test := range []struct {
		name, view string
		serve      http.HandlerFunc
		names      string
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
			names: "this API answers no record on documents, so it is older than this CLI",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.serve)
			t.Cleanup(server.Close)
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
			_, below, _ := strings.Cut(screen, "\n")
			if !strings.Contains(below, test.names) {
				t.Errorf("below the strip, the tab should say %q:\n%s", test.names, screen)
			}
		})
	}
}
