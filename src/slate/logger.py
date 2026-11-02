"""Structlog writes every record, its own and the stdlib's, to stdout.

`LOG_FORMAT` is `json` (the default, one object per line) or `console` for reading in a terminal.
`LOG_LEVEL` is a stdlib level name and defaults to `INFO`.
"""

import functools
import logging
import os
import sys

import structlog
from structlog.processors import CallsiteParameter
from structlog.processors import CallsiteParameterAdder
from structlog.typing import Processor

QUIET_LIBRARIES = {
    'httpcore': logging.INFO,
    'httpx': logging.WARNING,
    'urllib3': logging.INFO,
}


@functools.cache
def initialize_logging() -> None:
    log_format = os.environ.get('LOG_FORMAT') or 'json'
    log_level = os.environ.get('LOG_LEVEL') or 'INFO'
    shared_processors: list[Processor] = [
        structlog.contextvars.merge_contextvars,
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
