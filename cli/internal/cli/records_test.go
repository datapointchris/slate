package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/datapointchris/goselfupdate/autoupdate"

	"github.com/datapointchris/slate/cli/internal/api"
)

// One number sequence holds every kind, so `slate show` reads whichever kind
// holds the number through that kind's own renderer, and a worklog by the name
// it carries under its record's number. A kind added to api.RecordKinds with
// no case here fails, so `show` cannot meet a kind it has no renderer for.
func TestShowReadsEachKindByItsNumber(t *testing.T) {
	records := map[string]string{
		"/api/v1/records/1860":   `{"kind":"idea","number":1860,"record":{"number":1860,"title":"Ideas nobody took on","created_ts":"2026-10-01T00:00:00Z"}}`,
		"/api/v1/records/1501":   `{"kind":"initiative","number":1501,"record":{"id":"019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5c","number":1501,"name":"Ship the tracker","status":"active","created_ts":"2026-10-01T00:00:00Z"}}`,
		"/api/v1/records/412":    `{"kind":"issue","number":412,"record":{"id":"019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5b","number":412,"title":"Routing drops issues","status":"open","created_ts":"2026-10-01T00:00:00Z"}}`,
		"/api/v1/records/2950":   `{"kind":"pursuit","number":2950,"record":{"number":2950,"name":"Data Warehouse","summary":"All data flows through the warehouse","status":"active","created_ts":"2026-10-01T00:00:00Z","initiatives":[{"number":1501,"kind":"initiative","title":"Ship the tracker","status":"active"}]}}`,
		"/api/v1/worklogs/412.2": `{"name":"412.2","record_number":412,"sequence":2,"record":{"number":412,"kind":"issue","title":"Routing drops issues"},"opened_by":"claude-code/s","started_ts":"2026-10-01T00:00:00Z","state":"live","stage":"build"}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, ok := records[r.URL.Path]
		if !ok {
			body = `[]`
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	asService(t, serviceIDP(t).URL, server.URL, serviceSecret)

	cases := []struct{ arg, kind, want string }{
		{"1860", api.RecordKindIdea, "Ideas nobody took on"},
		{"1501", api.RecordKindInitiative, "Unclosed issues (0)"},
		{"412", api.RecordKindIssue, "Routing drops issues"},
		{"2950", api.RecordKindPursuit, "#1501 Ship the tracker (active)"},
		{"412.2", recordKindWorklog, "412.2  #412 Routing drops issues"},
	}
	for _, kind := range api.RecordKinds {
		if !slices.ContainsFunc(cases, func(c struct{ arg, kind, want string }) bool { return c.kind == kind }) {
			t.Errorf("no case shows a %s", kind)
		}
	}
	for _, c := range cases {
		out, err := runService(t, "show", c.arg)
		if err != nil || !strings.Contains(out, c.want) {
			t.Errorf("slate show %s = %q, %v; want it to print %q", c.arg, out, err, c.want)
		}
	}

	// A script re-reading what it was shown passes the envelope's identifier
	// back to `slate show`, so a worklog's is its name, never its record's number.
	out, err := runService(t, "show", "412.2", "--json")
	var envelope map[string]any
	if err != nil || json.Unmarshal([]byte(out), &envelope) != nil {
		t.Fatalf("slate show 412.2 --json = %q, %v", out, err)
	}
	if _, hasNumber := envelope["number"]; envelope["name"] != "412.2" || hasNumber {
		t.Errorf("slate show 412.2 --json names it %v and carries number %v; want name 412.2 and no number", envelope["name"], envelope["number"])
	}
}

// A folded idea's number and worklog names answer the record its rows went to,
// so `slate show` says which redirect it followed rather than showing #2356
// under the #4086 asked for. The API follows only for a client that asks, so
// the fake answers the redirect only when the request carries the header. A
// write through the old number is refused naming the record to write to.
func TestShowNamesTheRedirectItFollowed(t *testing.T) {
	refused := `{"error":"Conflict","message":"#4086 was folded into initiative #2356 on 2026-11-24; run it against #2356"}`
	answers := map[string]struct {
		status int
		body   string
	}{
		"GET /api/v1/records/4086": {http.StatusOK, `{"kind":"initiative","number":2356,"record":{"id":"01a1b283-9531-714b-adfc-f1891748c626","number":2356,"name":"Neutral name 1","status":"completed","created_ts":"2026-11-06T20:18:50Z",` +
			`"redirected_from":[{"number":4086,"reason":"folded","former_kind":"idea","former_title":"Neutral title 1","redirected_ts":"2026-11-24T12:00:00Z",` +
			`"sentence":"#4086 was folded into initiative #2356 on 2026-11-24"}]}}`},
		"GET /api/v1/worklogs/4086.1": {http.StatusOK, `{"name":"2356.6","record_number":2356,"sequence":6,"record":{"number":2356,"kind":"initiative","title":"Neutral name 1"},"opened_by":"claude-code/s","started_ts":"2026-11-20T00:00:00Z","state":"finished","stage":"shape"}`},
		"DELETE /api/v1/ideas/4086":   {http.StatusConflict, refused},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		answer, ok := answers[r.Method+" "+r.URL.Path]
		if !ok {
			answer.status, answer.body = http.StatusOK, `[]`
		}
		if r.URL.Path == "/api/v1/records/4086" && r.Header.Get(api.FollowRedirectsHeader) != "true" {
			answer.status, answer.body = http.StatusNotFound, `{"error":"Not Found","message":"record 4086 not found"}`
		}
		w.WriteHeader(answer.status)
		_, _ = w.Write([]byte(answer.body))
	}))
	t.Cleanup(server.Close)
	handToken(t, server.URL, "handed-access-token")

	out, err := runTreeOut(t, "show", "4086")
	for _, want := range []string{
		"#4086 was folded into initiative #2356 on 2026-11-24.\n",
		"Numbers leading here (1):\n  #4086  folded 2026-11-24  Neutral title 1",
	} {
		if err != nil || !strings.Contains(out, want) {
			t.Errorf("slate show 4086 = %q, %v; want it to print %q", out, err, want)
		}
	}
	if out, err := runTreeOut(t, "show", "4086.1"); err != nil || !strings.HasPrefix(out, "Worklog 4086.1 is now 2356.6.\n") {
		t.Errorf("slate show 4086.1 = %q, %v; want it to open on the worklog's new name", out, err)
	}
	_, err = runTreeOut(t, "ideas", "delete", "4086", "--yes")
	if want := "#4086 was folded into initiative #2356 on 2026-11-24; run it against #2356"; err == nil || err.Error() != want || exitCodeFor(err) != 1 {
		t.Errorf("slate ideas delete 4086 = %v, exit %d; want %q, exit 1", err, exitCodeFor(err), want)
	}
}

// runTreeOut drives the real tree through run, as Execute does, and returns
// what it printed to stdout beside the error Execute prints as `error:`.
func runTreeOut(t *testing.T, args ...string) (string, error) {
	t.Helper()
	original := os.Args
	os.Args = append([]string{"slate"}, args...)
	t.Cleanup(func() { os.Args = original })
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)
	err := run(root, autoupdate.Config{Suppress: true})
	return out.String(), err
}
