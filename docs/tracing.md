# Tracing

slate's API makes an OpenTelemetry span for every request, and the CLI hands
the API the trace it was started under. So a request lands under whatever
called it: an agent's tool call, a scheduled job, a shell script that set
`TRACEPARENT`. A filter on one record's number then finds every request that
touched that record, across every caller and every day it was worked.

## The API continues the caller's trace, and exports only where told

The OpenTelemetry SDK is configured from its own environment variables and
nothing else, so a deployment configures it the way it configures any other
service:

- `OTEL_TRACES_EXPORTER` names the exporters: `otlp`, `console` or `none`.
- Left unset, `otlp` runs when `OTEL_EXPORTER_OTLP_ENDPOINT` or
  `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` names a receiver. Otherwise nothing
  exports.
- The OTLP exporter speaks HTTP with protobuf, the specification's default
  protocol. The image carries no gRPC.
- `OTEL_SDK_DISABLED=true` turns the SDK off.

Exporting nothing is the default because the spec's default exporter, OTLP to
`localhost`, would retry an endpoint that is not there on every install that
never asked for tracing. Spans are still made with no exporter, so an incoming
`traceparent` is still continued. That puts the caller's trace id on every log
line and audit row even where no span leaves the process.

Each request's server span is named by its route's template, such as
`POST /api/v1/claims/{record}/extend`. It carries the stable HTTP conventions
(`http.request.method`, `http.response.status_code`). A deployment's own
`OTEL_SEMCONV_STABILITY_OPT_IN` outranks that default.

Each statement sent to Postgres is a child span, traced at the psycopg driver,
carrying its text in `db.statement` with placeholders and never the bound
values. The driver is traced rather than SQLAlchemy because OpenTelemetry's
SQLAlchemy instrumentation declares no support for SQLAlchemy 2.1, and
instruments nothing under it.

Every baggage entry the caller sends is copied onto every span. So a caller
that keeps a flow id in its baggage finds it on slate's spans, and slate's code
names no key of its own for it.

`service.name` is `slate`, and `service.version` is the release the image was
built for. `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES` outrank both.

## A span names the one record its route is about

A route that reads or writes one record puts its number on the request's span
as `slate.record.number`:

- the record in its path, whether the path names it by number, UUID or name
- the record in its body, as a hold, a comment or a new worklog names it
- the record a create made, once its number exists
- the record a `?record=` filter narrows a list to
- the record `POST /claims/next` took, which the caller could not name

`slate.worklog` names the worklog a route works in, and `session.id` names the
agent session a body or query names.

A route names one record and never a record it only references, such as the
issue a dependency points at or the initiative an issue files into. One
attribute with one value is what a filter can match, and a span answering "who
touched #12" should not also answer for every record #12 points at. A list or
search names no record.

`src/slate/tracing/named_work.py` holds the setters. A path's record is named
in the dependency that resolves it, so each route under it is covered once.

## Every other record a write changed is an event on its span

A write can change records it was not asked about. Closing an issue ends its
claim and can reopen its parent. Each such change is already in the write's
answer as a side effect, and each warning as a warning.

The span carries each one as an event: `slate.write_side_effect` or
`slate.write_warning`. Each event's `slate.write_effect.code` and
`slate.record.number` match the answer's `code` and `record`. So the parent a
close reopened is on that close's span, as an event. An event leaves the
answer's message out, since a message quotes titles, and a title is content a
trace store keeps longer than it should.

## A log line and an audit row carry the trace that made them

Every log line written under a span carries `trace_id`, `span_id` and
`trace_flags`, the fields an OpenTelemetry Collector's filelog receiver reads a
line's trace from. The request line also names the route's template, and the
`record`, `worklog` and `session` the route named. The setters keep those for
the line apart from the span, so a span the sampler drops still leaves the line
its record.

Every write's audit row keeps the `traceparent` and `baggage` of the span that
made it, beside its `request_id`. A record is read long after it was written,
by callers in traces of their own. Its changes carry the trace and flow that
made each one, so a reader can follow a change back to the request that made it.
`GET /records/{number}/changes` answers both.

A reader does not continue a writer's trace. A record filed today and claimed
in three days would put the claim under a trace that ended days before. The
number on both spans joins them instead.

## The CLI forwards its parent's trace and makes no span

The CLI reads `TRACEPARENT`, `TRACESTATE` and `BAGGAGE` from its environment at
startup, the specification's carriers for a trace handed to a child process.
Every API request then sends them on as its `traceparent` and `baggage`
headers.

The CLI makes no span of its own. Where something traced started it, that
caller's span is already the run's span, and the API's spans sit directly
under it. A span of the CLI's own would need the SDK and an exporter in the
binary, which nearly doubles its size, and an export to flush at every exit. A
CLI started with no `TRACEPARENT` sends none, so each of its requests starts a
trace of its own.
