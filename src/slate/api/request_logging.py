import time
import uuid

import structlog
from fastapi import Request
from starlette.middleware.base import BaseHTTPMiddleware

logger = structlog.get_logger()

# Probes arrive every few seconds, so logging them would bury every other line.
UNLOGGED_PATHS = frozenset({'/health', '/ready'})


class RequestLoggingMiddleware(BaseHTTPMiddleware):
    """Logs every request with its duration, under an `X-Request-ID` the audit trail also records."""

    async def dispatch(self, request: Request, call_next):
        structlog.contextvars.clear_contextvars()
        request_id = request.headers.get('X-Request-ID') or str(uuid.uuid4())[:8]
        structlog.contextvars.bind_contextvars(request_id=request_id)
        started = time.perf_counter()
        response = await call_next(request)
        response.headers['X-Request-ID'] = request_id
        if request.url.path not in UNLOGGED_PATHS:
            fields = {
                'method': request.method,
                'path': request.url.path,
                'status': response.status_code,
                'duration_ms': round((time.perf_counter() - started) * 1000, 2),
            }
            if response.status_code >= 500:
                logger.error('request_completed', **fields)
            elif response.status_code >= 400:
                logger.warning('request_completed', **fields)
            else:
                logger.info('request_completed', **fields)
        structlog.contextvars.clear_contextvars()
        return response
