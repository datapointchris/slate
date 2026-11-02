package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

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
