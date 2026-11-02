package cli

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A commit-msg hook stamps `Worklog: <name>` from what current prints, so it
// prints the name alone, and nothing at all when there is none to stamp.
func TestWorklogsCurrent_PrintsTheNameAloneOrNothing(t *testing.T) {
	var answer string
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.URL.Query().Get("session"); got != "s1" {
			t.Errorf("asked for session %q, want s1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(api.Close)
	asService(t, serviceIDP(t).URL, api.URL, serviceSecret)

	for _, c := range []struct{ answer, session, want string }{
		{`{"name":"1991.2","record_number":1991,"sequence":2,"state":"live","stage":"committed"}`, "s1", "1991.2\n"},
		{`null`, "s1", ""},
		{`unreached`, "", ""},
	} {
		answer = c.answer
		t.Setenv(claudeCodeSessionEnv, c.session)
		out, err := runService(t, "worklogs", "current")
		if err != nil || out != c.want {
			t.Errorf("session %q answered %s: printed %q (%v), want %q", c.session, c.answer, out, err, c.want)
		}
	}
	if calls != 2 {
		t.Errorf("the API was asked %d times, want 2: a run naming no session asks nothing", calls)
	}
}
