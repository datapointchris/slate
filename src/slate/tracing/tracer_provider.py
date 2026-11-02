"""The OpenTelemetry SDK, set up from its own environment variables and nothing else.

`OTEL_TRACES_EXPORTER` names the exporters: `otlp`, `console` or `none`. Left unset, `otlp` runs
when `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` names a receiver, and
nothing exports otherwise. So a deployment that sets nothing gets no exporter retrying a missing
endpoint. Spans are still made, so an incoming `traceparent` is continued and every log line and
audit row carries the caller's trace. `OTEL_SDK_DISABLED=true` turns the SDK off entirely.
"""

import functools
import importlib.metadata
import os
from collections.abc import Callable
from collections.abc import Iterable

import structlog
from fastapi import FastAPI
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
from opentelemetry.instrumentation.psycopg import PsycopgInstrumentor
from opentelemetry.processor.baggage import ALLOW_ALL_BAGGAGE_KEYS
from opentelemetry.processor.baggage import BaggageSpanProcessor
from opentelemetry.sdk.resources import SERVICE_NAME
from opentelemetry.sdk.resources import SERVICE_VERSION
from opentelemetry.sdk.resources import OTELResourceDetector
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.sdk.trace.export import ConsoleSpanExporter
from opentelemetry.sdk.trace.export import SpanExporter

logger = structlog.get_logger()

OTLP_ENDPOINT_VARIABLES = ('OTEL_EXPORTER_OTLP_ENDPOINT', 'OTEL_EXPORTER_OTLP_TRACES_ENDPOINT')

# One span per line, as the logs are one object per line, so both read from one stdout stream.
SPAN_EXPORTERS: dict[str, Callable[[], SpanExporter]] = {
    'otlp': OTLPSpanExporter,
    'console': lambda: ConsoleSpanExporter(formatter=lambda span: span.to_json(indent=None) + os.linesep),
}

# pyproject.toml's version for the source tree, which no release stamped.
UNRELEASED_VERSION = '0.0.0'


def exporter_names() -> list[str]:
    """The exporters `OTEL_TRACES_EXPORTER` names, else `otlp` where an OTLP endpoint is set, else none."""
    named = [name.strip() for name in (os.environ.get('OTEL_TRACES_EXPORTER') or '').split(',') if name.strip()]
    if not named:
        return ['otlp'] if any(os.environ.get(variable) for variable in OTLP_ENDPOINT_VARIABLES) else []
    return [name for name in named if name != 'none']


def service_resource() -> Resource:
    """`service.name` slate and the installed package's release, under what `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES` set.

    The image build stamps the release it builds into the package's version. A tree no release
    built keeps `UNRELEASED_VERSION`, and its spans carry no `service.version`.
    """
    defaults: dict[str, str] = {SERVICE_NAME: 'slate'}
    if (version := importlib.metadata.version('slate')) != UNRELEASED_VERSION:
        defaults[SERVICE_VERSION] = version
    return Resource.create(defaults).merge(OTELResourceDetector().detect())


def new_tracer_provider(exporters: Iterable[SpanExporter]) -> TracerProvider:
    """Spans with the caller's baggage on each, handed to every exporter in batches."""
    provider = TracerProvider(resource=service_resource())
    # Every baggage entry the caller sent lands on each span, so a flow id rides with no key named here.
    provider.add_span_processor(BaggageSpanProcessor(ALLOW_ALL_BAGGAGE_KEYS))
    for exporter in exporters:
        provider.add_span_processor(BatchSpanProcessor(exporter))
    return provider


def configured_exporters() -> list[SpanExporter]:
    exporters = []
    for name in exporter_names():
        if (exporter := SPAN_EXPORTERS.get(name)) is None:
            logger.warning('trace_exporter_unknown', exporter=name, known=sorted(SPAN_EXPORTERS))
            continue
        exporters.append(exporter())
    return exporters


@functools.cache
def initialize_tracing() -> TracerProvider:
    """Install the process's tracer provider, and a child span for each statement sent to Postgres.

    The stable HTTP and database conventions are the default, so a server span carries
    `http.request.method` and a statement's span `db.query.text`. A deployment's own
    `OTEL_SEMCONV_STABILITY_OPT_IN` outranks it. The default is set before any instrumentor runs,
    because the first one to run reads the variable once for the whole process.

    The statements are traced at the psycopg driver. OpenTelemetry's SQLAlchemy instrumentation, at
    0.66b2, declares support below SQLAlchemy 2.1 only, and instruments nothing on the 2.1 slate runs.
    """
    os.environ.setdefault('OTEL_SEMCONV_STABILITY_OPT_IN', 'http,database')
    provider = new_tracer_provider(configured_exporters())
    trace.set_tracer_provider(provider)
    PsycopgInstrumentor().instrument(tracer_provider=provider)
    logger.info('tracing_initialized', exporters=exporter_names())
    return provider


def instrument_api(api: FastAPI, tracer_provider: TracerProvider | None) -> None:
    """A server span per request, continuing the caller's `traceparent` and named by the route's template."""
    FastAPIInstrumentor.instrument_app(api, tracer_provider=tracer_provider, exclude_spans=['receive', 'send'])
