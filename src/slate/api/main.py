from collections.abc import AsyncGenerator
from collections.abc import Callable
from collections.abc import Mapping
from collections.abc import Sequence
from contextlib import asynccontextmanager
from http import HTTPStatus
from typing import Any

import structlog
from fastapi import APIRouter
from fastapi import Depends
from fastapi import FastAPI
from fastapi import status
from fastapi.encoders import jsonable_encoder
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from fastapi.routing import APIRoute
from fastapi.utils import is_body_allowed_for_status_code
from sqlalchemy import text
from starlette.exceptions import HTTPException
from starlette.responses import Response
from starlette.routing import Match

from slate.api import API_PREFIX
from slate.api.callers import get_current_caller
from slate.api.endpoints import claims
from slate.api.endpoints import ideas
from slate.api.endpoints import initiatives
from slate.api.endpoints import issues
from slate.api.endpoints import labels
from slate.api.endpoints import records
from slate.api.endpoints import sessions
from slate.api.endpoints import vocabulary
from slate.api.endpoints import worklogs
from slate.api.protected_resource import add_protected_resource_route
from slate.api.request_logging import RequestLoggingMiddleware
from slate.config import Settings
from slate.config import get_settings
from slate.database.session import get_db_engine
from slate.logger import initialize_logging

logger = structlog.get_logger()

VALIDATION_SCOPES = ('body', 'query', 'path', 'header', 'cookie')

# Every resource router and the prefix it serves under `API_PREFIX`, one noun each.
RESOURCE_ROUTERS: tuple[tuple[str, APIRouter], ...] = (
    ('/claims', claims.router),
    ('/ideas', ideas.router),
    ('/initiatives', initiatives.router),
    ('/issues', issues.router),
    ('/labels', labels.router),
    ('/records', records.router),
    ('/sessions', sessions.router),
    ('/vocabulary', vocabulary.router),
    ('/worklogs', worklogs.router),
)


def route_templates(routers: Sequence[tuple[str, APIRouter]]) -> dict[tuple[str, Callable[..., Any]], str]:
    """Each endpoint's full path template, by method and endpoint.

    FastAPI hands a dependency the route as its router declared it, without the prefixes it was
    included under, so the scope gate reads the template from this table instead.
    """
    return {
        (method, route.endpoint): f'{API_PREFIX}{prefix}{route.path}'
        for prefix, router in routers
        for route in router.routes
        if isinstance(route, APIRoute)
        for method in route.methods or ()
    }


def error_response(status_code: int, message: str, headers: Mapping[str, str] | None = None, **fields: Any) -> Response:
    """Answer an error as `{"error": <status phrase>, "message": <reason>}`.

    `message` is always a string, so a client reads one field to say what went wrong. Anything a
    caller reads beyond the reason rides beside the two.
    """
    if not is_body_allowed_for_status_code(status_code):
        return Response(status_code=status_code, headers=headers)
    try:
        phrase = HTTPStatus(status_code).phrase
    except ValueError:
        phrase = str(status_code)
    content = {'error': phrase, 'message': message, **jsonable_encoder(fields)}
    return JSONResponse(status_code=status_code, content=content, headers=headers)


def validation_message(errors: Sequence[dict[str, Any]]) -> str:
    """Each refused field as `field: reason`, the field dotted below the part of the request it came in."""
    parts = []
    for error in errors:
        loc = [str(segment) for segment in error.get('loc', ())]
        if loc and loc[0] in VALIDATION_SCOPES:
            loc = loc[1:]
        field = '.'.join(loc)
        parts.append(f'{field}: {error["msg"]}' if field else error['msg'])
    return '; '.join(parts)


def allowed_methods(request) -> str:
    """Every method a route on this path takes. Each method is its own route, and Starlette's `Allow` names only the first."""
    methods: set[str] = set()
    for route in request.app.routes:
        match, _ = route.matches(request.scope)
        if match != Match.NONE:
            methods.update(getattr(route, 'methods', None) or ())
    return ', '.join(sorted(methods))


async def http_exception_handler(request, exc):
    """A dict detail carries its reason under `message`, and each other key becomes a field beside it."""
    log = logger.error if exc.status_code >= status.HTTP_500_INTERNAL_SERVER_ERROR else logger.warning
    log('api_http_error', path=request.url.path, error=str(exc))
    if isinstance(exc.detail, dict):
        fields = dict(exc.detail)
        return error_response(exc.status_code, str(fields.pop('message', '')), exc.headers, **fields)
    # The router's own 404 and 405 carry only the status phrase, so these name the path instead.
    if exc.status_code == status.HTTP_404_NOT_FOUND and exc.detail == HTTPStatus.NOT_FOUND.phrase:
        return error_response(exc.status_code, f'no route answers {request.url.path}', exc.headers)
    if exc.status_code == status.HTTP_405_METHOD_NOT_ALLOWED and exc.detail == HTTPStatus.METHOD_NOT_ALLOWED.phrase:
        allow = allowed_methods(request)
        headers = {**(exc.headers or {}), 'Allow': allow}
        return error_response(exc.status_code, f'{request.url.path} takes {allow}, not {request.method}', headers)
    return error_response(exc.status_code, str(exc.detail), exc.headers)


async def request_validation_exception_handler(request, exc):
    """`errors` keeps FastAPI's list, so a client can mark each refused field."""
    logger.warning('api_validation_error', path=request.url.path, error=str(exc))
    errors = jsonable_encoder(exc.errors())
    return error_response(status.HTTP_422_UNPROCESSABLE_CONTENT, validation_message(errors), errors=errors)


async def unhandled_exception_handler(request, exc):
    logger.error('api_unhandled_error', path=request.url.path, error=str(exc), error_type=type(exc).__name__)
    return error_response(status.HTTP_500_INTERNAL_SERVER_ERROR, f'Internal server error: {type(exc).__name__}')


def add_probe_routes(api: FastAPI, settings: Settings) -> None:
    """`/health` answers while the process can serve, and `/ready` while Postgres answers too."""

    def health() -> dict[str, str]:
        return {'status': 'ok'}

    def ready() -> Response:
        try:
            with get_db_engine(settings).connect() as connection:
                connection.execute(text('SELECT 1'))
        except Exception as exc:
            logger.warning('not_ready', reason='postgres', error=str(exc))
            return JSONResponse(status_code=status.HTTP_503_SERVICE_UNAVAILABLE, content={'status': 'unavailable', 'postgres': str(exc)})
        return JSONResponse(content={'status': 'ok', 'postgres': 'ok'})

    api.add_api_route('/health', health, methods=['GET'], include_in_schema=False)
    api.add_api_route('/ready', ready, methods=['GET'], include_in_schema=False)


def create_api(settings: Settings) -> FastAPI:
    @asynccontextmanager
    async def lifespan(app: FastAPI) -> AsyncGenerator[None]:
        app.state.settings = settings
        logger.info('api_started')
        yield
        # uvicorn has stopped accepting connections and drained in-flight requests by now.
        get_db_engine(settings).dispose()
        logger.info('api_stopped')

    api = FastAPI(
        title=settings.fastapi.title,
        description=settings.fastapi.description,
        lifespan=lifespan,
        docs_url=f'{API_PREFIX}/docs',
        redoc_url=f'{API_PREFIX}/redoc',
        openapi_url=f'{API_PREFIX}/openapi.json',
    )
    api.add_middleware(RequestLoggingMiddleware)

    caller = [Depends(get_current_caller)]
    v1 = APIRouter(prefix=API_PREFIX)
    for prefix, router in RESOURCE_ROUTERS:
        v1.include_router(router, prefix=prefix, dependencies=caller)
    api.include_router(v1)
    api.state.route_templates = route_templates(RESOURCE_ROUTERS)
    add_probe_routes(api, settings)
    if settings.fastapi.public_url:
        add_protected_resource_route(api, settings.fastapi.public_url, settings.oidc.issuer)

    api.add_exception_handler(HTTPException, http_exception_handler)
    api.add_exception_handler(RequestValidationError, request_validation_exception_handler)
    api.add_exception_handler(Exception, unhandled_exception_handler)
    return api


def create_app() -> FastAPI:
    """The uvicorn factory: `uvicorn --factory slate.api.main:create_app`."""
    initialize_logging()
    return create_api(get_settings())
