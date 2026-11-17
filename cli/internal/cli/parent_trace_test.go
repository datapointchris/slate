package cli

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/baggage"
)

// A slate started under a trace sends it on with each API request, so the
// API's span sits under whatever ran slate and carries its flow. The session
// running slate rides in the baggage, unless the baggage already names one.
func TestAnAPIRequestCarriesTheTraceAndSessionSlateWasStartedUnder(t *testing.T) {
	const parent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	for _, c := range []struct {
		name, baggage, wantSession string
	}{
		{"the environment's session joins the baggage", "flow.id=flow-7", "2c3201ae"},
		{"the baggage's own session is kept", "flow.id=flow-7,fleet.session.id=parent-session", "parent-session"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var traceparent, sent string
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				traceparent, sent = r.Header.Get("traceparent"), r.Header.Get("baggage")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[]`))
			}))
			t.Cleanup(api.Close)
			handToken(t, api.URL, "handed-token")
			t.Setenv("TRACEPARENT", parent)
			t.Setenv("BAGGAGE", c.baggage)
			t.Setenv(claudeCodeSessionEnv, "2c3201ae")

			if err := runLine(t, "search", "sync"); err != nil {
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
