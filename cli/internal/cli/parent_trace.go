package cli

import (
	"context"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/contrib/propagators/envcar"
	"go.opentelemetry.io/otel/propagation"
)

// tracePropagator reads and writes W3C trace context and baggage, the default
// of OTEL_PROPAGATORS.
var tracePropagator = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})

// withParentTrace is ctx carrying the trace the process that started slate
// handed it in TRACEPARENT, TRACESTATE and BAGGAGE. slate makes no span of its
// own, so each API request's span sits directly under whatever ran it.
func withParentTrace(ctx context.Context) context.Context {
	return tracePropagator.Extract(ctx, &envcar.Carrier{})
}

// carryingTrace sends each request's trace and baggage on as its traceparent
// and baggage headers.
func carryingTrace(base http.RoundTripper) http.RoundTripper {
	return otelhttp.NewTransport(base, otelhttp.WithPropagators(tracePropagator))
}
