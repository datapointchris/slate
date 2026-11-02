"""Who is calling: a person's CLI or a scoped service client, each established by a verified token.

slate keeps no accounts. A person is whoever the identity provider says signed the token, and every
write they make records their `sub` as its actor. A service client is named by its client id.
"""

from typing import Annotated

import structlog
from fastapi import Depends
from fastapi import Request
from sqlalchemy.orm import Session

from slate.api.client_scopes import permits
from slate.api.client_scopes import reachable
from slate.api.exceptions import ForbiddenException
from slate.api.exceptions import Refusal
from slate.api.exceptions import UnauthorizedException
from slate.api.oidc_auth import OIDCIdentity
from slate.api.oidc_auth import ScopedClient
from slate.api.oidc_auth import get_oidc_identity
from slate.database.session import get_sqlalchemy_session
from slate.models.record_change import bind_actor

logger = structlog.get_logger()


def get_current_caller(
    request: Request,
    identity: Annotated[OIDCIdentity | ScopedClient | None, Depends(get_oidc_identity)],
    session: Annotated[Session, Depends(get_sqlalchemy_session)],
) -> OIDCIdentity | ScopedClient:
    """The verified caller, or 401 without a token and 403 for a scoped client outside its scopes."""
    if identity is None:
        raise UnauthorizedException(Refusal.INVALID_CREDENTIALS, logger)
    if isinstance(identity, ScopedClient):
        template = request.app.state.route_templates.get((request.method, request.scope.get('endpoint')))
        if not permits(identity.scopes, request.method, template):
            logger.warning('scoped_client_outside_scopes', client_id=identity.client_id, path=request.url.path)
            raise ForbiddenException(f'{Refusal.OUTSIDE_CLIENT_SCOPES}; {reachable(identity.scopes)}', logger)
        bind_actor(session, identity.client_id)
        logger.debug('auth_method_scoped_client', client_id=identity.client_id, path=request.url.path)
        return identity
    bind_actor(session, identity.subject)
    logger.debug('auth_method_person', subject=identity.subject, client_id=identity.client_id)
    return identity


def caller_actor(caller: OIDCIdentity | ScopedClient) -> str:
    """The name a caller's writes are signed with, as `bind_actor` records it."""
    return caller.client_id if isinstance(caller, ScopedClient) else caller.subject


def refuse_acting_for_another(caller: OIDCIdentity | ScopedClient, named: str, refusal: Refusal) -> None:
    """Refuse a scoped client naming anyone but itself, since its token says which service it is and nothing says which worker acted."""
    if isinstance(caller, ScopedClient) and named != caller.client_id:
        raise ForbiddenException(f'{refusal}, {caller.client_id}, not {named}', logger)


DbSession = Annotated[Session, Depends(get_sqlalchemy_session)]
CurrentCaller = Annotated[OIDCIdentity | ScopedClient, Depends(get_current_caller)]
