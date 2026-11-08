package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A dashboard that draws an initiative's bar beside slate tui's reads `initiatives board
// --json`, so the bar it prints is openKinds' split and the board keeps its list order.
func TestABoardReaderGetsEachInitiativesBarAsSlateTuiSplitsIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/initiatives/board" {
			t.Errorf("asked %s, want the initiatives board", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"read_ts": "2026-11-08T02:00:00Z", "zone": "UTC", "days": [], "tiers": [],
		  "totals": {"issue_count": 0, "open_count": 0, "states": {}, "closes": {"by_day": []}, "initiative_count": 0},
		  "initiatives": [
		    {"number": 9, "name": "Second", "issue_count": 12, "open_count": 9,
		     "states": {"completed": 2, "canceled": 1, "running": 2, "waiting": 1, "blocked": 3, "ready": 3},
		     "closes": {"by_day": []}, "issues": []},
		    {"number": 4, "name": "First", "issue_count": 1, "open_count": 0, "states": {"completed": 1},
		     "closes": {"by_day": []}, "issues": []}]}`))
	}))
	t.Cleanup(server.Close)
	asService(t, serviceIDP(t).URL, server.URL, serviceSecret)

	out, err := runService(t, "initiatives", "board", "--json")
	if err != nil {
		t.Fatalf("slate initiatives board --json: %v", err)
	}
	var printed struct {
		Initiatives []struct {
			Number int      `json:"number"`
			Bar    boardBar `json:"bar"`
		} `json:"initiatives"`
	}
	if err := json.Unmarshal([]byte(out), &printed); err != nil {
		t.Fatalf("read %q: %v", out, err)
	}
	want := []boardBar{{Done: 2, UnderWay: 3, Blocked: 3, Rest: 3}, {Done: 1}}
	if len(printed.Initiatives) != 2 || printed.Initiatives[0].Number != 9 ||
		printed.Initiatives[0].Bar != want[0] || printed.Initiatives[1].Bar != want[1] {
		t.Errorf("printed %+v, want 9 then 4 with bars %+v", printed.Initiatives, want)
	}
}
