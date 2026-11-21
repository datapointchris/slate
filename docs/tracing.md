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

A stopping API waits `SHUTDOWN_FLUSH_SECONDS` for its queued spans to export,
then drops what is left. The SDK's own shutdown takes no timeout, and against a
receiver that is down it outlasts the time a stopping pod has before it is
killed.

slate sets `OTEL_SEMCONV_STABILITY_OPT_IN=http,database` where a deployment
leaves it unset. A deployment's own value outranks it. Under that default:

- Each request's server span is named by its route's template, such as
  `POST /api/v1/claims/{record}/extend`, and carries `http.request.method`,
  `http.route` and `http.response.status_code`.
- Each statement sent to Postgres is a child span, traced at the psycopg
  driver. It carries its text in `db.query.text`, with placeholders and never
  the bound values.

The driver is traced rather than SQLAlchemy because OpenTelemetry's SQLAlchemy
instrumentation, at 0.66b2, declares support below SQLAlchemy 2.1 only. It
instruments nothing on the 2.1 slate runs.

Every baggage entry the caller sends is copied onto every span, whatever its
key. So a caller that keeps a flow id in its baggage finds it on slate's spans.
The two members slate reads itself, to keep a write's flow, are in "A record
keeps the flow of the work that created it" below.

`service.name` is `slate`. `service.version` is the installed package's
version, which the image build sets to the release it builds. A tree no release
built carries no `service.version`. `OTEL_SERVICE_NAME` and
`OTEL_RESOURCE_ATTRIBUTES` outrank both.

## A span names the one record its route is about

A route that reads or writes one record puts its number on the request's span
as `slate.record.number`:

- the record in its path, whether the path names it by number, UUID or name
- the record in its body, as a hold, a comment or a new worklog names it
- the record a create made, once its number exists
- the record a filter narrows a list to: `?record=`, an issue list's
  `?initiative=` or `?parent=`, and the one initiative a progress read names
- the record `POST /claims/next` took, which the caller could not name

Where two filters each name a record, the narrower one is named: a parent over
an initiative, and a worklog's record over `?record=`.

`slate.worklog` names the worklog a route works in. `session.id` names the
agent session a route's path, body or query names.

A route names one record and never a record it only references, such as the
issue a dependency points at or the initiative an issue files into. One
attribute with one value is what a filter can match, and a span answering "who
touched #12" should not also answer for every record #12 points at. A list
that no filter narrows to one record names none, and a search names none.

`src/slate/tracing/named_work.py` holds the setters. A path's record is named
in the dependency that resolves it, so each route under it is covered once.

## Every other record a write changed is an event on its span

A write can change records it was not asked about. Closing an issue ends its
claim, and reopening one reopens each completed ancestor. Each such change is
already in the write's answer as a side effect, and each warning as a warning.

The span carries each one as an event: `slate.write_side_effect` or
`slate.write_warning`. Each event's `slate.write_effect.code` and
`slate.record.number` match the answer's `code` and `record`. So an ancestor a
reopen reopened is on that reopen's span, as an event. An event leaves the
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
made it, beside its `request_id`, and the write's `flow_id`.
`GET /records/{number}/changes` answers all three.

A process that acts on a record later starts a trace of its own, with a span
link to the `traceparent` of the change it acts on. It carries that change's
`baggage` on, so the flow that filed the record continues. It does not continue
the writer's trace, because a record filed today and claimed in three days
would put the claim under a trace that ended days before.

The API's own spans add no link. Each continues its caller's trace, and the
record's number on each joins every trace that touched the record.

## A record keeps the flow of the work that created it

A flow is one cause's life: everything done because of one report, request or
idea. Its id has a W3C trace id's form, 32 lowercase hex characters, not all
zeros. A value in any other form reads as no flow.

A request's flow is the first of these it has:

1. the flow of the worklog the writing agent session is in, from that
   session's open span. The request names its session in the baggage member
   `fleet.session.id`.
2. its `fleet.flow.id` baggage member
3. its trace id, or a fresh one where it carries no trace

The session's worklog comes first because a session's baggage is fixed when it
starts. A session that claims another record keeps its first baggage, while
its open span moves to the new worklog.

The baggage member alone decides the flow. A write that also names a session in
its body, as a claim, a comment or a document version does, takes its worklog
from the body. So a client names the same session in both, as the CLI does.

The API takes the session as the request names it, as it does every body
`session` field. A token names a person or a service, never an agent session.
So a row's `flow_id` is what its writer asserted, a key for grouping work rather
than proof of who did it.

Each row below keeps a flow as `flow_id`:

- A record of any kind takes the request's flow when it is created, and keeps
  it. An initiative promoted from an idea takes the idea's flow instead.
- A worklog takes its record's flow, whatever flow the session that opened it
  works under, because it is one go at that record's work.
- A claim, a comment, a document version and an audit row take the request's
  flow rather than one copied from their record. Where the writing session was
  already in another record's worklog, that flow is the other record's. A
  session in no worklog writes under its baggage flow or its trace id, which
  need not be the record's.

An idea, initiative or issue filed before the column existed holds a root flow
minted for it once, and each worklog opened on it before then holds that flow.
So every go at one record shares one flow. An initiative promoted from such an
idea shares the idea's, as a promotion does now. `flow_id` is null on any other row
written before the column existed: a pursuit, a claim, a comment, a document
version or an audit row.

The flow is resolved in the request's first transaction and set as the
transaction-local setting `audit.flow_id`. The audit trigger copies it onto
each audit row, and every `flow_id` column that keeps its writer's flow
defaults to it. So a row inserted by any path keeps it, with no code at the
insert. The request keeps that flow to its end. A hold moves the session's span
mid-request, and the rows written after it are still the work of the flow the
request arrived under.

`slate show <number> --json` answers a record's flow at `.record.flow_id`, and
`slate worklogs current --json` its worklog's at `.flow_id`. Every read of the
rows above answers it too.

## The CLI forwards its parent's trace and makes no span

The CLI reads `TRACEPARENT`, `TRACESTATE` and `BAGGAGE` from its environment at
startup, the specification's carriers for a trace handed to a child process.
Every API request then sends them on as its `traceparent`, `tracestate` and
`baggage` headers. The baggage member `fleet.session.id` names the agent session the
command acts as: its `--session` where it takes one, else
`CLAUDE_CODE_SESSION_ID`. That replaces any member the CLI inherited, because
the session running a command is the one writing, not the one that started it.
An empty `--session` sends no member. Where neither names a session, an
inherited member stands. The API reads the member to find the session's
worklog.

The CLI makes no span of its own. Where something traced started it, that
caller's span is already the run's span, and the API's spans sit directly
under it. A span of the CLI's own would need the Go SDK and an OTLP exporter in
the binary. At SDK v1.47.0 those measured 8.3 MB on a 10.1 MB binary, and they
add an export to flush at every exit. A CLI started with no `TRACEPARENT` sends
none, so each of its requests starts a trace of its own.

## The Claude session `slate tui`'s C opens carries its record on its resource

An interactive Claude session ignores an incoming `TRACEPARENT`, and each of
its prompts starts a trace of its own. So C joins the session to its record
and to the flow `slate tui` runs in through `OTEL_RESOURCE_ATTRIBUTES` instead.
The session's resource keeps the attributes `slate tui` inherited, adds
`slate.record.number`, the name a span gives the record, and adds each member
of the inherited `BAGGAGE` as it came. `BAGGAGE` itself goes on as well, so the
session's own calls carry the flow. Inside tmux these reach the session through
`env` ahead of `claude`, because a tmux window takes its environment from the
tmux server rather than from the program that opened it.
