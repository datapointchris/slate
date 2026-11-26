"""Structlog writes every record, its own and the stdlib's, to stdout.

`LOG_FORMAT` is `json` (the default, one object per line) or `console` for reading in a terminal.
`LOG_LEVEL` is a stdlib level name and defaults to `INFO`.

A line written under a span carries `trace_id`, `span_id` and `trace_flags`, the fields an
OpenTelemetry Collector's filelog receiver reads a line's trace from.

A few events are also OTLP log records, each emitted through the process's `LoggerProvider`, such
as a hard-to-read-sentence check that got no answer. The provider exports over OTLP/HTTP where
`OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` names a receiver, and drops
them otherwise.
"""

import functools
import logging
import os
import sys
import threading

import structlog
from opentelemetry import trace
from opentelemetry._logs import set_logger_provider
from opentelemetry.exporter.otlp.proto.http._log_exporter import OTLPLogExporter
from opentelemetry.sdk._logs import LoggerProvider
from opentelemetry.sdk._logs.export import BatchLogRecordProcessor
from opentelemetry.trace import format_span_id
from opentelemetry.trace import format_trace_id
from structlog.processors import CallsiteParameter
from structlog.processors import CallsiteParameterAdder
from structlog.typing import EventDict
from structlog.typing import Processor
from structlog.typing import WrappedLogger

from slate.tracing.tracer_provider import service_resource

QUIET_LIBRARIES = {
    'httpcore': logging.INFO,
    'httpx': logging.WARNING,
    'urllib3': logging.INFO,
}

OTLP_LOG_ENDPOINT_VARIABLES = ('OTEL_EXPORTER_OTLP_ENDPOINT', 'OTEL_EXPORTER_OTLP_LOGS_ENDPOINT')

# A receiver that is down never takes the records, so a longer wait only holds a stopping pod.
SHUTDOWN_FLUSH_SECONDS = 5


def add_trace_context(_logger: WrappedLogger, _method: str, event_dict: EventDict) -> EventDict:
    context = trace.get_current_span().get_span_context()
    if context.is_valid:
        event_dict['trace_id'] = format_trace_id(context.trace_id)
        event_dict['span_id'] = format_span_id(context.span_id)
        event_dict['trace_flags'] = f'{context.trace_flags:02x}'
    return event_dict


@functools.cache
def initialize_logging() -> None:
    log_format = os.environ.get('LOG_FORMAT') or 'json'
    log_level = os.environ.get('LOG_LEVEL') or 'INFO'
    shared_processors: list[Processor] = [
        structlog.contextvars.merge_contextvars,
        add_trace_context,
        structlog.processors.add_log_level,
        structlog.processors.TimeStamper(fmt='iso', utc=True),
        CallsiteParameterAdder({CallsiteParameter.FILENAME, CallsiteParameter.FUNC_NAME, CallsiteParameter.LINENO}),
        structlog.processors.StackInfoRenderer(),
        structlog.processors.format_exc_info,
    ]
    # structlog renders through the stdlib root logger, so alembic's and uvicorn's records reach the same handler.
    logging.basicConfig(format='%(message)s', level=getattr(logging, log_level), stream=sys.stdout, force=True)
    structlog.configure(
        processors=[*shared_processors, structlog.stdlib.ProcessorFormatter.wrap_for_formatter],
        wrapper_class=structlog.stdlib.BoundLogger,
        context_class=dict,
        logger_factory=structlog.stdlib.LoggerFactory(),
        cache_logger_on_first_use=True,
    )
    renderer = structlog.processors.JSONRenderer() if log_format == 'json' else structlog.dev.ConsoleRenderer(colors=sys.stdout.isatty())
    formatter = structlog.stdlib.ProcessorFormatter(processor=renderer, foreign_pre_chain=shared_processors)
    for handler in logging.root.handlers:
        handler.setFormatter(formatter)
    for name, level in QUIET_LIBRARIES.items():
        logging.getLogger(name).setLevel(level)
    # uvicorn configures these before it calls the app factory, with a plain-text handler that does not propagate.
    # uvicorn.access is left alone: the request middleware logs each request, and `--no-access-log` silences it.
    for name in ('uvicorn', 'uvicorn.error'):
        logging.getLogger(name).handlers.clear()
        logging.getLogger(name).propagate = True


def new_logger_provider(exporters: list[OTLPLogExporter]) -> LoggerProvider:
    """Log records under slate's service resource, handed to every exporter in batches."""
    provider = LoggerProvider(resource=service_resource())
    for exporter in exporters:
        provider.add_log_record_processor(BatchLogRecordProcessor(exporter))
    return provider


@functools.cache
def initialize_log_export() -> LoggerProvider:
    """Install the process's logger provider, exporting over OTLP/HTTP only where an endpoint is set."""
    exporting = any(os.environ.get(variable) for variable in OTLP_LOG_ENDPOINT_VARIABLES)
    provider = new_logger_provider([OTLPLogExporter()] if exporting else [])
    set_logger_provider(provider)
    structlog.get_logger().info('log_export_initialized', exporting=exporting)
    return provider


def shut_down_log_export(provider: LoggerProvider) -> None:
    """Export what is queued and stop, waiting `SHUTDOWN_FLUSH_SECONDS` at most, as the tracer's shutdown does."""
    flushing = threading.Thread(target=provider.shutdown, name='log-flush', daemon=True)
    flushing.start()
    flushing.join(SHUTDOWN_FLUSH_SECONDS)
    if flushing.is_alive():
        structlog.get_logger().warning('log_flush_abandoned', waited_seconds=SHUTDOWN_FLUSH_SECONDS)
