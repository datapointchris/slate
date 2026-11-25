package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/gotermboard"
	"github.com/datapointchris/gotermstyle"

	"github.com/datapointchris/slate/cli/internal/api"
)

// pursuitsStandIn stands in for slate's focus and pursuits routes. Focus 3 is in force over
// three days, naming 2984 and 2983, and completed 2992 drew output on the last two alone. Focus 2
// ran before it and answers its last two days of 82. noFocus answers no focus in force, and
// usageFails a 500 on the usage by day.
type pursuitsStandIn struct {
	mu                  sync.Mutex
	noFocus, usageFails bool
	usageAsked          []string
}

func onDay(day int) time.Time { return time.Date(2026, 11, day, 0, 0, 0, 0, time.Local) }

func standingFigures(number int, share *float64, named bool, usage float64, factor *float64, output float64) api.PursuitFigures {
	return api.PursuitFigures{Number: number, Share: share, ShareNamed: named, SplitOutputTokens: output, WorklogCount: 1, Usage: usage, Factor: factor}
}

// usageDay is a day of the read: each pursuit's figures over its window, ending at the next
// midnight, or at 15:00 on the 23rd, the read.
func usageDay(day int, figures ...api.PursuitFigures) api.FocusUsageDay {
	until := onDay(day + 1)
	if day == 23 {
		until = onDay(23).Add(15 * time.Hour)
	}
	listed := api.FocusUsageDay{Day: onDay(day).Format(time.DateOnly), Window: api.FocusWindow{Since: onDay(21), Until: until}}
	for _, f := range figures {
		listed.Pursuits = append(listed.Pursuits, api.PursuitUsageOnDay{PursuitFigures: f, SplitOutputTokensOnDay: f.SplitOutputTokens / 3})
	}
	return listed
}

func (s *pursuitsStandIn) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := map[int]string{2983: "Finish the warehouse", 2984: "Lock the doors", 2985: "Tidy the shed", 2989: "Talk to the neighbors", 2992: "Paint the fence"}
	named := func(number int) api.PursuitSummary { return api.PursuitSummary{Number: number, Name: names[number]} }
	current := api.Focus{
		ID: 3, Statement: "Lock the doors up front, then finish the warehouse", StartedTS: onDay(21).Add(9 * time.Hour),
		UntilDate: "2026-12-23", SetBy: "chris", Active: true, HeldUntil: new(time.Date(2026, 12, 24, 0, 0, 0, 0, time.Local)),
		Shares: []api.FocusShare{{Pursuit: named(2984), Share: 0.45}, {Pursuit: named(2983), Share: 0.25}},
	}
	before := api.Focus{
		ID: 2, Statement: "Warehouse first", StartedTS: time.Date(2026, 9, 1, 9, 0, 0, 0, time.Local), UntilDate: "2026-11-30",
		EndedTS: new(onDay(21).Add(9 * time.Hour)), EndedReason: new("replaced"), SetBy: "chris", HeldUntil: new(onDay(21).Add(9 * time.Hour)),
		Shares: []api.FocusShare{{Pursuit: named(2983), Share: 0.6}},
	}
	var day21, day22, day23 []api.PursuitFigures
	if s.noFocus {
		day22 = []api.PursuitFigures{
			standingFigures(2985, nil, false, 0, nil, 0), standingFigures(2983, nil, false, 0.4, nil, 2000), standingFigures(2984, nil, false, 0.6, nil, 3000),
		}
		day23 = []api.PursuitFigures{
			standingFigures(2985, nil, false, 0, nil, 0), standingFigures(2983, nil, false, 0.3, nil, 3000), standingFigures(2984, nil, false, 0.7, nil, 7000),
		}
	} else {
		day21 = []api.PursuitFigures{
			standingFigures(2985, new(0.15), false, 0, new(1.0), 0), standingFigures(2984, new(0.45), true, 0.02, new(0.97), 1000),
			standingFigures(2983, new(0.25), true, 0.03, new(0.92), 1500), standingFigures(2989, new(0.15), false, 0.95, new(0.01), 47500),
		}
		day22 = []api.PursuitFigures{
			standingFigures(2985, new(0.15), false, 0, new(1.0), 0), standingFigures(2984, new(0.45), true, 0.05, new(0.93), 30000),
			standingFigures(2983, new(0.25), true, 0.05, new(0.87), 30000), standingFigures(2989, new(0.15), false, 0.7, new(0.04), 420000),
			standingFigures(2992, nil, false, 0.2, nil, 120000),
		}
		day23 = []api.PursuitFigures{
			standingFigures(2985, new(0.15), false, 0, new(1.0), 0), standingFigures(2984, new(0.45), true, 0.08, new(0.88), 125300),
			standingFigures(2983, new(0.25), true, 0.07, new(0.82), 105300), standingFigures(2989, new(0.15), false, 0.65, new(0.05), 704000),
			standingFigures(2992, nil, false, 0.2, nil, 298600),
		}
	}
	var answer any
	switch r.URL.Path {
	case "/api/v1/focus":
		standing := api.FocusStanding{Window: api.FocusWindow{Since: onDay(21), Until: onDay(23).Add(15 * time.Hour)}}
		if !s.noFocus {
			standing.Focus = &current
		}
		for _, figures := range day23 {
			status := "active"
			if figures.Number == 2992 {
				status = "completed"
			}
			standing.Pursuits = append(standing.Pursuits, api.PursuitStanding{PursuitFigures: figures, Name: names[figures.Number], Status: status, TouchedTS: new(onDay(23).Add(9 * time.Hour))})
		}
		answer = standing
	case "/api/v1/pursuits":
		var pursuits []api.Pursuit
		for _, number := range []int{2983, 2984, 2985, 2989, 2992} {
			pursuit := api.Pursuit{Number: number, Name: names[number], Summary: names[number] + " holds", Status: "active"}
			for initiative := range number % 4 {
				pursuit.Initiatives = append(pursuit.Initiatives, api.RecordSummary{Number: 100 + initiative, Kind: api.RecordKindInitiative})
			}
			pursuits = append(pursuits, pursuit)
		}
		answer = pursuits
	case "/api/v1/focus/history":
		if s.noFocus {
			current.Active, current.EndedTS, current.EndedReason = false, new(onDay(22)), new("the audit finished early")
		}
		answer = []api.Focus{current, before}
	case "/api/v1/focus/usage":
		s.usageAsked = append(s.usageAsked, r.URL.Query().Get("focus"))
		if s.usageFails {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"Internal Server Error","message":"the usage read timed out"}`))
			return
		}
		usage := api.FocusUsage{Since: onDay(21).Add(9 * time.Hour), Until: onDay(23).Add(15 * time.Hour), Days: []api.FocusUsageDay{
			usageDay(21, day21...), usageDay(22, day22...), usageDay(23, day23...),
		}}
		switch {
		case r.URL.Query().Get("focus") == "2":
			usage = api.FocusUsage{Focus: &before, Since: onDay(20), Until: onDay(21).Add(9 * time.Hour), DaysInFocus: new(82), Days: []api.FocusUsageDay{
				usageDay(20, standingFigures(2983, new(0.6), true, 0.4, new(0.63), 4000), standingFigures(2984, new(0.2), false, 0.6, new(0.13), 6000)),
				usageDay(21,
					standingFigures(2983, new(0.6), true, 0.5, new(0.56), 5000), standingFigures(2984, new(0.2), false, 0.3, new(0.35), 3000),
					standingFigures(2985, new(0.2), false, 0.2, new(0.5), 2000)),
			}}
		case s.noFocus:
			usage = api.FocusUsage{Since: onDay(22), Until: onDay(23).Add(15 * time.Hour), Days: []api.FocusUsageDay{usageDay(22, day22...), usageDay(23, day23...)}}
		default:
			usage.Focus, usage.DaysInFocus = &current, new(3)
		}
		for _, number := range []int{2983, 2984, 2985, 2989, 2992} {
			listed := slices.ContainsFunc(usage.Days, func(day api.FocusUsageDay) bool {
				return slices.ContainsFunc(day.Pursuits, func(p api.PursuitUsageOnDay) bool { return p.Number == number })
			})
			if listed {
				usage.Pursuits = append(usage.Pursuits, api.FocusUsagePursuit{PursuitSummary: named(number), Status: "active"})
			}
		}
		answer = usage
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}

// openPursuitsTab is the pursuits tab on stand, wide enough for the side panel, with every read
// landed.
func openPursuitsTab(t *testing.T, stand *pursuitsStandIn) (*pursuitsBoard, *gotermboard.Model, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(stand.serve))
	t.Cleanup(server.Close)
	board := newPursuitsBoard(api.New(server.URL, server.Client()))
	model := gotermboard.NewModel(board, gotermstyle.NewPalette(false))
	model.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	settle(model, model.Init())
	return board, model, server
}

// panelOf is the side panel's text: each line past the table's edge.
func panelOf(screen string) string {
	var panel []string
	for line := range strings.SplitSeq(screen, "\n") {
		if _, beside, ok := strings.Cut(line, "│ "); ok {
			panel = append(panel, beside)
		}
	}
	return strings.Join(panel, "\n")
}

// drawsBraille says text holds a braille dot, which a line of a chart draws in.
func drawsBraille(text string) bool {
	return strings.ContainsFunc(text, func(r rune) bool { return r > 0x2800 && r <= 0x28FF })
}

// figuresOf is the row of the screen naming number, split at its spaces.
func figuresOf(screen string, number string) []string {
	for line := range strings.SplitSeq(screen, "\n") {
		if fields := strings.Fields(strings.TrimLeft(line, "▌ ")); len(fields) > 0 && fields[0] == number {
			return fields
		}
	}
	return nil
}

func TestThePursuitsTabSplitsTheStandingAndChartsEachPursuitByDay(t *testing.T) {
	stand := &pursuitsStandIn{}
	board, model, server := openPursuitsTab(t, stand)

	screen := model.View().Content
	order := []string{"named shares · 2", "  2984 ", "  2983 ", "the rest share 0.30 · 3", "  2985 ", "  2989 ", "  2992 "}
	if at := linesAt(screen, order); !slices.IsSorted(at) || slices.Contains(at, -1) {
		t.Fatalf("the tab should draw the named shares, then the rest, each in the standing's order, at lines %v:\n%s", at, screen)
	}
	if !strings.Contains(cursorLine(screen), "Lock the doors") {
		t.Errorf("the cursor should start on the first named share:\n%s", screen)
	}
	panel := panelOf(screen)
	for _, want := range []string{"usage by day, focus 3", "·· share", "── usage", "12-19"} {
		if !strings.Contains(panel, want) {
			t.Errorf("the panel should chart 2984's usage by day beside its share through the focus's end, naming %q:\n%s", want, panel)
		}
	}
	if !drawsBraille(panel) {
		t.Errorf("the panel should draw the chart's lines:\n%s", panel)
	}

	model.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	board.usage.Refresh(context.Background())
	model.Rebuild()
	screen = model.View().Content
	strip := strings.SplitN(screen, "\n", 2)[0]
	if !strings.Contains(strip, "focus 2 · 09-01 to 11-21 · replaced · the last 2 of its 82 days · Warehouse first") {
		t.Errorf("h should show focus 2's span, end, cut and statement: %s", strip)
	}
	if got := figuresOf(screen, "2983"); !slices.Equal(got[:4], []string{"2983", "0.60", "0.50", "0.56"}) || !strings.Contains(screen, "named shares · 1") {
		t.Errorf("h should draw focus 2's shares and its last day's usage and factor, drew %v:\n%s", got, screen)
	}
	if asked := stand.usageAsked; asked[len(asked)-1] != "2" {
		t.Errorf("h should read focus 2's usage by day, read %q", asked)
	}

	asService(t, serviceIDP(t).URL, server.URL, serviceSecret)
	out, err := runService(t, "tui", "--view", tabPursuits, "--json")
	if err != nil {
		t.Fatal(err)
	}
	validateAgainst(t, "tui-pursuits.schema.json", out)
	var printed pursuitsSnapshot
	if err := json.Unmarshal([]byte(out), &printed); err != nil {
		t.Fatal(err)
	}
	if len(printed.Rows) != 5 || printed.Rows[0].Number != 2985 || printed.Rows[1].Group != pursuitGroupNamed || len(printed.Rows[1].ByDay) != 3 ||
		len(printed.Rows[4].ByDay) != 2 {
		t.Errorf("--json should print each pursuit in the standing's order with each day that lists it:\n%s", out)
	}
}

func TestThePursuitsTabWithNoFocusDrawsOneGroupAndNoShareLine(t *testing.T) {
	_, model, _ := openPursuitsTab(t, &pursuitsStandIn{noFocus: true})
	screen := model.View().Content
	if !strings.Contains(screen, "every pursuit · 3") || strings.Contains(screen, "named shares") {
		t.Errorf("one group should hold every pursuit:\n%s", screen)
	}
	if got := figuresOf(screen, "2984"); !slices.Equal(got[:4], []string{"2984", "·", "0.70", "·"}) {
		t.Errorf("SHARE and FACTOR should read · beside the usage, drew %v:\n%s", got, screen)
	}
	panel := panelOf(screen)
	if !strings.Contains(panel, "── usage") || strings.Contains(panel, "share   ") || !drawsBraille(panel) {
		t.Errorf("the panel should chart usage with no share line:\n%s", panel)
	}
}

func TestAFailedUsageReadSaysUnreadRatherThanDrawingALine(t *testing.T) {
	_, model, _ := openPursuitsTab(t, &pursuitsStandIn{usageFails: true})
	screen := model.View().Content
	panel := panelOf(screen)
	if !strings.Contains(panel, "usage by day unread: API request failed (500") || drawsBraille(panel) {
		t.Errorf("the panel should say the usage by day is unread, and draw no line:\n%s", panel)
	}
	if got := figuresOf(screen, "2984"); len(got) < 4 || got[1] != "0.45" {
		t.Errorf("the rows should still draw the standing, drew %v:\n%s", got, screen)
	}
}
