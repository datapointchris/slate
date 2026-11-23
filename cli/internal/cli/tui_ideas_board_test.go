package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/gotermboard"
	"github.com/datapointchris/gotermstyle"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/datapointchris/slate/cli/internal/api"
)

func TestTheIdeasTabListsUnpromotedIdeasNewestFirst(t *testing.T) {
	day := func(n int) time.Time { return time.Date(2026, 11, n, 12, 0, 0, 0, time.UTC) }
	garden, reading := api.PursuitSummary{Number: 3010, Name: "Grow the garden"}, api.PursuitSummary{Number: 3011, Name: "Read more"}
	compost := "Turn the kitchen scraps into compost by spring."
	ideas := []api.Idea{
		{Number: 3700, Title: "A shelf for the library books", Priority: 3, CreatedTS: day(18), UpdatedTS: day(18), InPursuits: api.InPursuits{Pursuits: []api.PursuitSummary{reading}}},
		{
			Number: 4089, Title: "Compost the kitchen scraps", Description: &compost, Priority: 3, CreatedTS: day(21), UpdatedTS: day(21),
			Tags: []string{"outdoors"}, InPursuits: api.InPursuits{Pursuits: []api.PursuitSummary{garden}},
			Refs: []api.RecordRef{{Ref: "capture/01a20512", Kind: "capture", LinkedTS: day(21)}},
		},
		{Number: 4164, Title: "Dated reminders", CreatedTS: day(22), UpdatedTS: day(22)},
		{Number: 3500, Title: "Promoted already", CreatedTS: day(23), UpdatedTS: day(23), Initiative: &api.Initiative{Number: 3600}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ideas" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ideas)
	}))
	t.Cleanup(server.Close)

	asService(t, serviceIDP(t).URL, server.URL, serviceSecret)
	out, err := runService(t, "tui", "--view", "ideas", "--json")
	if err != nil {
		t.Fatal(err)
	}
	validateAgainst(t, "tui-ideas.schema.json", out)
	var printed ideasSnapshot
	if err := json.Unmarshal([]byte(out), &printed); err != nil {
		t.Fatal(err)
	}
	numbers := func(rows []ideaSnapshotRow) []int {
		listed := []int{}
		for _, row := range rows {
			listed = append(listed, row.Number)
		}
		return listed
	}
	if got := numbers(printed.Rows); !slices.Equal(got, []int{4164, 4089, 3700}) || printed.Unpromoted != 3 {
		t.Errorf("--json lists %v of %d unpromoted, want 4164, 4089 and 3700 of 3", got, printed.Unpromoted)
	}
	if row := printed.Rows[1]; row.RefCount != 1 || !slices.Equal(row.Tags, []string{"outdoors"}) || row.Pursuits[0] != garden {
		t.Errorf("4089 prints %d refs, tags %v and pursuits %v", row.RefCount, row.Tags, row.Pursuits)
	}

	board := newIdeasBoard(api.New(server.URL, server.Client()))
	if err := board.readOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	model := gotermboard.NewModel(board, gotermstyle.NewPalette(false))
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model.Update(tea.KeyPressMsg{Code: 'u', Text: "u"})
	if got := numbers(board.Snapshot(time.Now()).Rows); !slices.Equal(got, []int{4089}) || board.pursuit == nil || *board.pursuit != garden {
		t.Fatalf("u narrows to %v under %v, want 4089 alone under Grow the garden", got, board.pursuit)
	}
	if screen := model.View().Content; strings.Contains(screen, compost) || !strings.Contains(screen, "pursuit Grow the garden") {
		t.Fatalf("narrowed and closed, the tab should name its pursuit and hold 4089's read shut:\n%s", screen)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if screen := model.View().Content; !strings.Contains(screen, compost) || !strings.Contains(screen, "capture/01a20512") {
		t.Errorf("enter on 4089 should open its read with its description and refs:\n%s", screen)
	}
}

// validateAgainst fails the test where document does not hold to the JSON Schema in file.
func validateAgainst(t *testing.T, file, document string) {
	t.Helper()
	text, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	schemaJSON, err := jsonschema.UnmarshalJSON(bytes.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(file, schemaJSON); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(file)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := jsonschema.UnmarshalJSON(strings.NewReader(document))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(instance); err != nil {
		t.Fatalf("%v\n%s", err, document)
	}
}
