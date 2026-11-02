"""Verification of the RFC 9068 JWT access tokens the identity provider issues to slate's clients.

A person's CLI logs in with the OAuth 2.0 device authorization grant. A service running the CLI with
no person present uses the client-credentials grant instead. Both tokens are signed JWTs, and the
API verifies them in-process against the provider's JWKS, so no proxy in front of it needs to.

The `client_id` prefix decides which of the two a token is: `slate-cli-` is a person, and
`slate-svc-` is a `ScopedClient`. A missing `sub` cannot decide it. Authelia 4.39 leaves `sub` off a
client-credentials token, but RFC 9068 requires it, and a release that adds it would make a service
token look like a person's.

Key retrieval, caching and rotation are PyJWT's `PyJWKClient`. Nothing here parses a JWT by hand.
It caches the key set for its lifespan and never a key by itself, because a per-kid cache holds a
retired key for the life of the process.
"""

import functools
from typing import Annotated

import httpx2
import jwt
import structlog
from fastapi import Depends
from fastapi import HTTPException
from fastapi import Request
from fastapi import status
from pydantic import BaseModel

from slate.config import Settings
from slate.config import get_settings

logger = structlog.get_logger()

ACCESS_TOKEN_TYPE = 'at+jwt'
SIGNING_ALGORITHM = 'RS256'
DISCOVERY_PATH = '/.well-known/openid-configuration'
DISCOVERY_TIMEOUT_SECONDS = 10.0

# A CDN in front of a provider can answer 403 to an unidentified client. Cloudflare refuses
# `Python-urllib/3.12`, which urllib and therefore PyJWKClient send by default, and serves a named
# agent. Naming the caller is the fix, because reaching the provider by an internal address instead
# would tie token verification to one network layout.
USER_AGENT = 'slate-api (+https://github.com/datapointchris/slate)'

# Every rejection returns this, so a probe cannot use the response to learn which check it failed.
# The reason is logged instead.
UNAUTHORIZED_DETAIL = 'Invalid credentials'
PROVIDER_UNAVAILABLE_DETAIL = 'Identity provider unavailable'


class OIDCVerificationError(Exception):
    """A token failed one of the checks. The message is for the log, never for the caller."""


class OIDCProviderUnavailable(Exception):
    """The identity provider could not be reached, so the token was never judged.

    Distinct from a rejection: answering 401 here tells a caller holding a good token to log in
    again, and a re-login cannot fix a network failure.
    """


class OIDCIdentity(BaseModel):
    """What a verified `slate-cli-` access token establishes about the person calling."""

    subject: str
    client_id: str


class ScopedClient(BaseModel):
    """A verified `slate-svc-` access token, which speaks for no person and reaches only the routes its scopes list."""

    client_id: str
    scopes: frozenset[str]


def bearer_token(authorization_header: str) -> str:
    """Return the credential from an Authorization header, or '' for an absent or non-Bearer one."""
    scheme, _, credential = authorization_header.partition(' ')
    if scheme.lower() != 'bearer':
        return ''
    return credential.strip()


def is_access_token(token: str) -> bool:
    """Report whether the token declares itself an RFC 9068 access token.

    This routes rather than authorizes. A bearer that is not an access token reaches no identity,
    so the request answers 401. The same check runs again inside `OIDCTokenVerifier.verify`, where
    it is load-bearing.
    """
    try:
        header = jwt.get_unverified_header(token)
    except jwt.PyJWTError:
        return False
    return str(header.get('typ', '')).lower() == ACCESS_TOKEN_TYPE


def discover_jwks_uri(issuer: str, timeout: float = DISCOVERY_TIMEOUT_SECONDS) -> str:
    """Resolve the issuer's JWKS endpoint from its OIDC discovery document."""
    response = httpx2.get(issuer.rstrip('/') + DISCOVERY_PATH, timeout=timeout, follow_redirects=False, headers={'User-Agent': USER_AGENT})
    response.raise_for_status()
    document = response.json()

    if document.get('issuer') != issuer:
        raise ValueError(f'issuer {issuer!r} advertises itself as {document.get("issuer")!r}')
    if not (jwks_uri := document.get('jwks_uri')):
        raise ValueError(f'issuer {issuer!r} advertises no jwks_uri')
    return jwks_uri


class OIDCTokenVerifier:
    """Checks a token's signature against the issuer's JWKS and its claims against this API."""

    def __init__(self, issuer: str, cli_client_id_prefix: str, service_client_id_prefix: str, jwk_client: jwt.PyJWKClient) -> None:
        self.issuer = issuer
        self.cli_client_id_prefix = cli_client_id_prefix
        self.service_client_id_prefix = service_client_id_prefix
        self.jwk_client = jwk_client

    @classmethod
    def from_discovery(cls, issuer: str, cli_client_id_prefix: str, service_client_id_prefix: str) -> OIDCTokenVerifier:
        jwks_uri = discover_jwks_uri(issuer)
        client = jwt.PyJWKClient(jwks_uri, headers={'User-Agent': USER_AGENT})
        return cls(issuer, cli_client_id_prefix, service_client_id_prefix, client)

    def verify(self, token: str) -> OIDCIdentity | ScopedClient:
        """Verify a token and return who it establishes, or raise OIDCVerificationError."""
        self._require_access_token_type(token)

        try:
            signing_key = self.jwk_client.get_signing_key_from_jwt(token)
        except jwt.exceptions.PyJWKClientConnectionError as exc:
            raise OIDCProviderUnavailable(f'JWKS fetch failed: {exc}') from exc
        except jwt.PyJWTError as exc:
            raise OIDCVerificationError(f'no signing key for this token: {exc}') from exc

        try:
            claims = jwt.decode(
                token,
                signing_key.key,
                algorithms=[SIGNING_ALGORITHM],
                issuer=self.issuer,
                # `aud` is empty on every token the device grant produces, so there is nothing to
                # match and PyJWT would reject a token whose aud it cannot be told to expect.
                # `sub` is checked per client kind below, because a service token carries none.
                options={'verify_aud': False, 'require': ['exp', 'iss']},
            )
        except jwt.PyJWTError as exc:
            raise OIDCVerificationError(f'{type(exc).__name__}: {exc}') from exc

        client_id = claims.get('client_id')
        # A non-string claim would raise AttributeError out of startswith, which is not a rejection
        # and would surface as a 500 rather than the opaque 401 every other failure returns.
        if not isinstance(client_id, str):
            raise OIDCVerificationError(f'client_id {client_id!r} is not a string')
        if client_id.startswith(self.cli_client_id_prefix):
            return self._person(claims, client_id)
        if client_id.startswith(self.service_client_id_prefix):
            return self._scoped_client(claims, client_id)
        raise OIDCVerificationError(f'client {client_id!r} is neither {self.cli_client_id_prefix}* nor {self.service_client_id_prefix}*')

    @staticmethod
    def _person(claims: dict, client_id: str) -> OIDCIdentity:
        subject = claims.get('sub') or ''
        if not subject:
            raise OIDCVerificationError('token carries no subject')
        return OIDCIdentity(subject=subject, client_id=client_id)

    @staticmethod
    def _scoped_client(claims: dict, client_id: str) -> ScopedClient:
        # Authelia sends `scp` as a list, and `[]` for a request that named no scope.
        scopes = claims.get('scp')
        if not isinstance(scopes, list) or not scopes or not all(isinstance(scope, str) for scope in scopes):
            raise OIDCVerificationError(f'service client {client_id!r} carries no scopes: {scopes!r}')
        return ScopedClient(client_id=client_id, scopes=frozenset(scopes))

    def _require_access_token_type(self, token: str) -> None:
        """Reject anything not typed as an RFC 9068 access token.

        An id_token from the same issuer carries a valid signature and the same issuer, so without
        this check it would authenticate — and unlike the access token it is handed to the client
        rather than kept from it.
        """
        try:
            header = jwt.get_unverified_header(token)
        except jwt.PyJWTError as exc:
            raise OIDCVerificationError(f'token header is unreadable: {exc}') from exc

        token_type = str(header.get('typ', ''))
        if token_type.lower() != ACCESS_TOKEN_TYPE:
            raise OIDCVerificationError(f'token type {token_type!r} is not {ACCESS_TOKEN_TYPE}')


@functools.cache
def build_verifier(issuer: str, cli_client_id_prefix: str, service_client_id_prefix: str) -> OIDCTokenVerifier:
    """Return the process-wide verifier for an issuer, resolving discovery on first use.

    Cached on the strings rather than on Settings so a failed discovery is not memoized —
    `functools.cache` stores results, not exceptions, so a provider down at the first request does
    not poison every later one.
    """
    return OIDCTokenVerifier.from_discovery(issuer, cli_client_id_prefix, service_client_id_prefix)


def get_oidc_identity(request: Request, settings: Annotated[Settings, Depends(get_settings)]) -> OIDCIdentity | ScopedClient | None:
    """FastAPI dependency resolving a verified access token to a person or a scoped client.

    Returns None when the request carries no access token. A request presenting one that fails
    verification raises 401 here, with the reason logged and never sent.
    """
    token = bearer_token(request.headers.get('Authorization', ''))
    if not token or not is_access_token(token):
        return None

    try:
        verifier = build_verifier(settings.oidc.issuer, settings.oidc.cli_client_id_prefix, settings.oidc.service_client_id_prefix)
        identity = verifier.verify(token)
    except (OIDCProviderUnavailable, httpx2.HTTPError) as exc:
        logger.error('oidc_provider_unreachable', error=str(exc), path=request.url.path)
        raise HTTPException(status_code=status.HTTP_503_SERVICE_UNAVAILABLE, detail=PROVIDER_UNAVAILABLE_DETAIL) from None
    except (OIDCVerificationError, ValueError) as exc:
        logger.warning('oidc_bearer_rejected', error=str(exc), path=request.url.path)
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail=UNAUTHORIZED_DETAIL) from None

    if isinstance(identity, ScopedClient):
        logger.debug('oidc_bearer_verified_scoped_client', client_id=identity.client_id, scopes=sorted(identity.scopes))
    else:
        logger.debug('oidc_bearer_verified', subject=identity.subject, client_id=identity.client_id)
    return identity
