"""One line per request, logged once its response is sent or its handler raised.

It is plain ASGI middleware, so a handler raising past every exception handler still logs its 500
here before the server's error handler answers it. The line names the route's template, and the
record, worklog and agent session the route named.
"""

import time
import uuid

import structlog
from starlette.datastructures import Headers
from starlette.datastructures import MutableHeaders
from starlette.types import ASGIApp
from starlette.types import Message
from starlette.types import Receive
from starlette.types import Scope
from starlette.types import Send

from slate.tracing.named_work import open_named_work

logger = structlog.get_logger()

# Probes arrive every few seconds, so logging them would bury every other line.
UNLOGGED_PATHS = frozenset({'/health', '/ready'})


class RequestLoggingMiddleware:
    """Logs every request with its duration, under an `X-Request-ID` the audit trail also records."""

    def __init__(self, app: ASGIApp) -> None:
        self.app = app

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope['type'] != 'http':
            await self.app(scope, receive, send)
            return
        structlog.contextvars.clear_contextvars()
        request_id = Headers(scope=scope).get('x-request-id') or str(uuid.uuid4())[:8]
        structlog.contextvars.bind_contextvars(request_id=request_id)
        named = open_named_work()
        started = time.perf_counter()
        # A handler that raises sends nothing, and the server's error handler answers 500.
        status = 500

        async def send_with_request_id(message: Message) -> None:
            nonlocal status
            if message['type'] == 'http.response.start':
                status = message['status']
                MutableHeaders(scope=message).append('X-Request-ID', request_id)
            await send(message)

        try:
            await self.app(scope, receive, send_with_request_id)
        finally:
            if scope['path'] not in UNLOGGED_PATHS:
                log_request(scope, status, round((time.perf_counter() - started) * 1000, 2), named)


def log_request(scope: Scope, status: int, duration_ms: float, named: dict[str, str | int]) -> None:
    fields: dict[str, str | int | float] = {'method': scope['method'], 'path': scope['path'], 'status': status, 'duration_ms': duration_ms}
    templates = getattr(scope['app'].state, 'route_templates', {})
    if (route := templates.get((scope['method'], scope.get('endpoint')))) is not None:
        fields['route'] = route
    fields.update(named)
    if status >= 500:
        logger.error('request_completed', **fields)
    elif status >= 400:
        logger.warning('request_completed', **fields)
    else:
        logger.info('request_completed', **fields)
