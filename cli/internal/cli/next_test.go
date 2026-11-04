package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// `slate next --for 1544` is a coordinator reading its whole plan, so the default cap
// of ten rows would hide work it has to run.
func TestNextForAPlanShowsEveryRowAndWhatEachUnblocks(t *testing.T) {
	rows := make([]string, defaultNextLimit+2)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"kind": "issue", "number": %d, "title": "Stage %d", "step": "build", "priority": 2, "unblocks": [2242],
		  "record": {"number": %d, "title": "Stage %d", "status": "open", "priority": 2, "effective_priority": 2}}`, 3000+i, i, 3000+i, i)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/queue" || r.URL.Query().Get("for_record") != "1544" {
			t.Errorf("asked %s, want the queue for 1544", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"queue": [` + strings.Join(rows, ",") + `], "claimed": []}`))
	}))
	t.Cleanup(server.Close)
	asService(t, serviceIDP(t).URL, server.URL, serviceSecret)

	out, err := runService(t, "next", "--for", "1544")
	if err != nil {
		t.Fatalf("slate next --for 1544: %v", err)
	}
	if !strings.Contains(out, fmt.Sprintf("Stage %d", len(rows)-1)) || !strings.Contains(out, "unblocks #2242") {
		t.Errorf("the plan printed:\n%s", out)
	}
}
