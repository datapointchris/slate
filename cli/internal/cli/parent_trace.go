package cli

import (
	"context"
	"net/http"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/contrib/propagators/envcar"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
)

// tracePropagator reads and writes W3C trace context and baggage, the default
// of OTEL_PROPAGATORS.
var tracePropagator = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})

// sessionBaggageKey is the baggage member naming the agent session a request
// acts as. The API reads it to find the worklog that session is in, and
// resolves a write's flow from it first. docs/tracing.md gives the rest.
const sessionBaggageKey = "fleet.session.id"

// actingSessionAnnotation marks the --session flag naming the session a
// command acts as, apart from a --session that filters a list.
const actingSessionAnnotation = "slate_acting_session"

// withParentTrace is ctx carrying the trace the process that started slate
// handed it in TRACEPARENT, TRACESTATE and BAGGAGE. slate makes no span of its
// own, so each API request's span sits directly under whatever ran it.
func withParentTrace(ctx context.Context) context.Context {
	return tracePropagator.Extract(ctx, &envcar.Carrier{})
}

// addActingSessionFlag adds the --session naming the agent session a command
// acts as, defaulting to the session running slate. Each request the command
// sends names the same session in its baggage.
func addActingSessionFlag(f *pflag.FlagSet, session *string, usage string) {
	f.StringVar(session, "session", os.Getenv(claudeCodeSessionEnv), usage)
	_ = f.SetAnnotation("session", actingSessionAnnotation, []string{"true"})
}

// actingSession is the agent session cmd acts as: its acting --session where it
// takes one, else the session running slate. named is false where neither
// names one, which leaves the baggage slate inherited as it is.
func actingSession(cmd *cobra.Command) (session string, named bool) {
	if f := cmd.Flags().Lookup("session"); f != nil && f.Annotations[actingSessionAnnotation] != nil {
		return f.Value.String(), true
	}
	session = os.Getenv(claudeCodeSessionEnv)
	return session, session != ""
}

// withSessionBaggage is ctx with session as its sessionBaggageKey member,
// replacing one it inherited, or with none where session is empty.
func withSessionBaggage(ctx context.Context, session string) context.Context {
	bag := baggage.FromContext(ctx)
	if session == "" {
		return baggage.ContextWithBaggage(ctx, bag.DeleteMember(sessionBaggageKey))
	}
	member, err := baggage.NewMemberRaw(sessionBaggageKey, session)
	if err != nil {
		return ctx
	}
	if bag, err = bag.SetMember(member); err != nil {
		return ctx
	}
	return baggage.ContextWithBaggage(ctx, bag)
}

// carryingTrace sends each request's trace and baggage on as its traceparent
// and baggage headers, the baggage naming the session cmd acts as.
func carryingTrace(cmd *cobra.Command, base http.RoundTripper) http.RoundTripper {
	traced := otelhttp.NewTransport(base, otelhttp.WithPropagators(tracePropagator))
	session, named := actingSession(cmd)
	if !named {
		return traced
	}
	return namingSession{next: traced, session: session}
}

// namingSession puts its session in each request's baggage before the trace
// transport writes the baggage header.
type namingSession struct {
	next    http.RoundTripper
	session string
}

func (t namingSession) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.next.RoundTrip(req.WithContext(withSessionBaggage(req.Context(), t.session)))
}
