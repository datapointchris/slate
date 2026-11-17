package cli

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/baggage"
)

// A slate started under a trace sends it on with each API request, so the
// API's span sits under whatever ran slate and carries its flow. The baggage
// names the session the command acts as: its --session where it takes one,
// else the session running slate, over any session it inherited.
func TestAnAPIRequestCarriesTheTraceAndTheSessionItActsAs(t *testing.T) {
	const parent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	for _, c := range []struct {
		name, baggage, wantSession string
		args                       []string
	}{
		{"the running session joins the baggage", "flow.id=flow-7", "2c3201ae", []string{"search", "sync"}},
		{"the running session replaces an inherited one", "flow.id=flow-7,fleet.session.id=parent", "2c3201ae", []string{"search", "sync"}},
		{"a command's --session names it", "flow.id=flow-7", "named", []string{"comments", "add", "412", "--body", "x", "--session", "named"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var traceparent, sent string
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				traceparent, sent = r.Header.Get("traceparent"), r.Header.Get("baggage")
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`[]`))
					return
				}
				_, _ = w.Write([]byte(`{"comment":{"id":1,"record_number":412}}`))
			}))
			t.Cleanup(api.Close)
			handToken(t, api.URL, "handed-token")
			t.Setenv("TRACEPARENT", parent)
			t.Setenv("BAGGAGE", c.baggage)
			t.Setenv(claudeCodeSessionEnv, "2c3201ae")

			if err := runLine(t, c.args...); err != nil {
				t.Fatal(err)
			}
			bag, err := baggage.Parse(sent)
			if err != nil {
				t.Fatalf("the API saw baggage %q: %v", sent, err)
			}
			if traceparent != parent || bag.Member("flow.id").Value() != "flow-7" || bag.Member(sessionBaggageKey).Value() != c.wantSession {
				t.Errorf("the API saw traceparent %q and baggage %q, want %q with flow.id=flow-7 and %s=%s",
					traceparent, sent, parent, sessionBaggageKey, c.wantSession)
			}
		})
	}
}
