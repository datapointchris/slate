package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// documentsStandIn stands in for slate's documents routes. It lists the documents of
// initiative 4271 and issue 4281 interleaved, newest written first, as the API does, and counts
// 120 records holding one, so + has more to read. 4271/design is at designVersion, with one
// line comment on its fifth line.
type documentsStandIn struct {
	mu            sync.Mutex
	designVersion int
	recordLimits  []string
	bodyReads     map[string]int
}

func (s *documentsStandIn) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := func(hour int) time.Time { return time.Date(2026, 11, 24, hour, 0, 0, 0, time.UTC) }
	initiative := api.RecordSummary{Number: 4271, Kind: api.RecordKindInitiative, Title: "Reminders fire on a date", Status: ptr("active")}
	issue := api.RecordSummary{Number: 4281, Kind: api.RecordKindIssue, Title: "The desk nudge pill", Status: ptr("open")}
	listed := func(record api.RecordSummary, name, kind string, version, hour, comments int) api.ListedDocument {
		return api.ListedDocument{Record: record, DocumentSummary: api.DocumentSummary{
			RecordNumber: record.Number, Name: name, Address: strconv.Itoa(record.Number) + "/" + name, Kind: kind, CreatedTS: at(9),
			Latest: api.DocumentVersionSummary{Version: version, WrittenBy: "someone", WrittenTS: at(hour), Worklog: ptr("4271.2"), LineCount: 5}, CommentCount: comments,
		}}
	}
	var answer any
	switch r.URL.Path {
	case "/api/v1/documents":
		s.recordLimits = append(s.recordLimits, r.URL.Query().Get("record_limit"))
		w.Header().Set("X-Total-Records", "120")
		answer = []api.ListedDocument{
			listed(initiative, "design", "design", s.designVersion, 14, 1),
			listed(issue, "design", "design", 1, 13, 0),
			listed(initiative, "plan", "plan", 1, 12, 0),
			listed(initiative, "design-review", "review", 1, 11, 0),
		}
	case "/api/v1/documents/4271/design":
		s.bodyReads["4271/design"]++
		body := "# The reminders design\n\nVersion " + strconv.Itoa(s.designVersion) + " fires on a date.\n\nReadiness reads ready."
		answer = api.Document{
			Record: initiative, Name: "design", Address: "4271/design", Kind: "design", CreatedTS: at(9),
			Version: api.DocumentVersion{
				DocumentVersionSummary: api.DocumentVersionSummary{Version: s.designVersion, WrittenBy: "someone", WrittenTS: at(14), LineCount: 5},
				Body:                   body, Lines: strings.Split(body, "\n"),
			},
			Comments: []api.PlacedComment{{
				Comment: api.Comment{
					ID: 7, RecordNumber: 4271, Body: "Check this again after 4270 lands.", WrittenBy: ptr("chris"), WrittenTS: at(15),
					On: &api.CommentLine{Document: "design", Kind: "design", Version: 1, Line: 5, Text: "Readiness reads ready."},
				},
				ShownLine: ptr(5),
			}},
		}
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}

func (s *documentsStandIn) reads(address string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bodyReads[address]
}

func TestTheDocumentsTabGroupsByRecordAndReadsTheLatestVersionOnce(t *testing.T) {
	ctx := context.Background()
	stand := &documentsStandIn{designVersion: 1, bodyReads: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(stand.serve))
	t.Cleanup(server.Close)
	board := newDocumentsBoard(api.New(server.URL, server.Client()))
	model := gotermboard.NewModel(board, gotermstyle.NewPalette(false))
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	settle(model, model.Init())

	screen := model.View().Content
	order := []string{"4271  Reminders fire on a date", "design ", "plan ", "design-review", "4281  The desk nudge pill"}
	if at := linesAt(screen, order); !slices.IsSorted(at) || slices.Contains(at, -1) {
		t.Fatalf("the tab should draw 4271's documents newest first, then 4281, at lines %v:\n%s", at, screen)
	}
	if !strings.Contains(cursorLine(screen), "design") {
		t.Fatalf("the cursor should start on 4271/design, the document written last:\n%s", screen)
	}

	// The cursor's landing forced the body read, which the next tick makes.
	board.bodies.Refresh(ctx)
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	reader := model.View().Content
	for _, want := range []string{"Version 1 fires on a date.", "1 line comment", "line 5", `"Readiness reads ready."`, "Check this again after 4270 lands."} {
		if !strings.Contains(reader, want) {
			t.Errorf("enter on 4271/design should read %q:\n%s", want, reader)
		}
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	board.list.Refresh(ctx)
	board.bodies.Refresh(ctx)
	if reads := stand.reads("4271/design"); reads != 1 {
		t.Fatalf("a list showing the version kept should read no body, but 4271/design was read %d times", reads)
	}
	stand.designVersion = 2
	board.list.Refresh(ctx)
	board.bodies.Refresh(ctx)
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if reads, reader := stand.reads("4271/design"), model.View().Content; reads != 2 || !strings.Contains(reader, "Version 2 fires on a date.") {
		t.Fatalf("a list showing version 2 should read the body again, read %d times:\n%s", reads, reader)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	if strip := strings.SplitN(model.View().Content, "\n", 2)[0]; !strings.Contains(strip, "2 of 120 records · 4 documents") || !strings.Contains(strip, "+ reads 50 more") {
		t.Errorf("the strip should name the records past the bound, and + as what reads them: %s", strip)
	}
	model.Update(tea.KeyPressMsg{Code: '+', Text: "+"})
	board.list.Refresh(ctx)
	if limits := stand.recordLimits; limits[len(limits)-1] != "100" {
		t.Errorf("+ should read the documents on 100 records, read %v", limits)
	}

	asService(t, serviceIDP(t).URL, server.URL, serviceSecret)
	out, err := runService(t, "tui", "--view", "documents", "--json")
	if err != nil {
		t.Fatal(err)
	}
	validateAgainst(t, "tui-documents.schema.json", out)
	var printed documentsSnapshot
	if err := json.Unmarshal([]byte(out), &printed); err != nil {
		t.Fatal(err)
	}
	if len(printed.Records) != 2 || printed.Records[0].Record.Number != 4271 || len(printed.Records[0].Documents) != 3 ||
		printed.CursorDocument == nil || printed.CursorDocument.Address != "4271/design" || len(printed.CursorDocument.Comments) != 1 {
		t.Errorf("--json should print 4271's three documents first and open 4271/design with its comment:\n%s", out)
	}
}

// linesAt is the line of screen each of texts first shows on, -1 where none does.
func linesAt(screen string, texts []string) []int {
	lines := strings.Split(screen, "\n")
	at := make([]int, len(texts))
	for i, text := range texts {
		at[i] = slices.IndexFunc(lines, func(line string) bool { return strings.Contains(line, text) })
	}
	return at
}
