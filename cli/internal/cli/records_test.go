package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// One number sequence holds every kind, so `slate show` reads whichever kind
// holds the number through that kind's own renderer.
func TestShowReadsEachKindByItsNumber(t *testing.T) {
	records := map[string]string{
		"/api/v1/records/1860": `{"kind":"idea","number":1860,"record":{"number":1860,"title":"Ideas nobody took on","created_ts":"2026-10-01T00:00:00Z"}}`,
		"/api/v1/records/1501": `{"kind":"initiative","number":1501,"record":{"id":"019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5c","number":1501,"name":"Ship the tracker","status":"active","created_ts":"2026-10-01T00:00:00Z"}}`,
		"/api/v1/records/412":  `{"kind":"issue","number":412,"record":{"id":"019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5b","number":412,"title":"Routing drops issues","status":"open","created_ts":"2026-10-01T00:00:00Z"}}`,
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, ok := records[r.URL.Path]
		if !ok {
			body = `[]`
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(api.Close)
	asService(t, serviceIDP(t).URL, api.URL, serviceSecret)

	for number, want := range map[string]string{
		"1860": "Ideas nobody took on",
		"1501": "Unclosed issues (0)",
		"412":  "Routing drops issues",
	} {
		out, err := runService(t, "show", number)
		if err != nil || !strings.Contains(out, want) {
			t.Errorf("slate show %s = %q, %v; want it to print %q", number, out, err, want)
		}
	}
}
