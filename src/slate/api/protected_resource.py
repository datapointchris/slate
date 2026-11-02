"""The RFC 9728 metadata `slate auth login <url>` reads before it holds a token.

It names the issuer the CLI logs in through. The CLI refuses the document unless
`resource` matches the URL it was given exactly, so `resource` is the address the
edge serves this API at, which no request can be trusted to say.
"""

from fastapi import FastAPI

WELL_KNOWN = '/.well-known/oauth-protected-resource'


def add_protected_resource_route(api: FastAPI, resource: str, issuer: str) -> None:
    """Serve the metadata for `resource` without authentication."""
    document: dict[str, object] = {'resource': resource, 'authorization_servers': [issuer]}

    def protected_resource() -> dict[str, object]:
        return document

    api.add_api_route(WELL_KNOWN, protected_resource, methods=['GET'], include_in_schema=False)
