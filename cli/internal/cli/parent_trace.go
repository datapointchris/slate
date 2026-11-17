package cli

import (
	"context"
	"net/http"
	"os"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/contrib/propagators/envcar"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
)

// tracePropagator reads and writes W3C trace context and baggage, the default
// of OTEL_PROPAGATORS.
var tracePropagator = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})

// sessionBaggageKey is the baggage member naming the agent session that runs
// slate. The API reads it to find the worklog that session is in, whose flow
// each record the request writes takes.
const sessionBaggageKey = "fleet.session.id"

// withParentTrace is ctx carrying the trace the process that started slate
// handed it in TRACEPARENT, TRACESTATE and BAGGAGE, and the session
// CLAUDE_CODE_SESSION_ID names where that baggage names none. slate makes no
// span of its own, so each API request's span sits directly under whatever
// ran it.
func withParentTrace(ctx context.Context) context.Context {
	ctx = tracePropagator.Extract(ctx, &envcar.Carrier{})
	return withSessionBaggage(ctx, os.Getenv(claudeCodeSessionEnv))
}

// withSessionBaggage is ctx with session as its sessionBaggageKey member,
// unless session is empty or the baggage already names one.
func withSessionBaggage(ctx context.Context, session string) context.Context {
	bag := baggage.FromContext(ctx)
	if session == "" || bag.Member(sessionBaggageKey).Key() != "" {
		return ctx
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
// and baggage headers.
func carryingTrace(base http.RoundTripper) http.RoundTripper {
	return otelhttp.NewTransport(base, otelhttp.WithPropagators(tracePropagator))
}
