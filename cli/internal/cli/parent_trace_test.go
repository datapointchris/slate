package cli

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A slate started under a trace sends it on with each API request, so the
// API's span sits under whatever ran slate and carries its flow.
func TestAnAPIRequestCarriesTheTraceSlateWasStartedUnder(t *testing.T) {
	const parent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	var traceparent, baggage string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceparent, baggage = r.Header.Get("traceparent"), r.Header.Get("baggage")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(api.Close)
	handToken(t, api.URL, "handed-token")
	t.Setenv("TRACEPARENT", parent)
	t.Setenv("BAGGAGE", "flow.id=flow-7")

	if err := runLine(t, "search", "sync"); err != nil {
		t.Fatal(err)
	}
	if traceparent != parent || baggage != "flow.id=flow-7" {
		t.Errorf("the API saw traceparent %q and baggage %q, want %q and flow.id=flow-7", traceparent, baggage, parent)
	}
}
