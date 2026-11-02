"""The routes each client scope reaches, as (method, route template) pairs.

A `ScopedClient` reaches a route only when one of its scopes lists it here, and every other route
answers it 403. A template is FastAPI's with every router prefix included, so
`/api/v1/issues/{id}` covers every issue.
"""

from itertools import chain

from slate.api import API_PREFIX

# The issue update reads these two to decide how far it narrows a scoped client's body.
ISSUES_CLOSE = 'slate.issues.close'
ISSUES_PLAN = 'slate.issues.plan'

SCOPE_ROUTES: dict[str, frozenset[tuple[str, str]]] = {
    # The reads `slate issues list`, `search`, `next`, `show`, and `initiatives list` and `show`
    # make. An issue in a series often has no description of its own, so a reader judging it
    # needs its initiative's.
    'slate.issues.read': frozenset(
        {
            ('GET', f'{API_PREFIX}/issues'),
            ('GET', f'{API_PREFIX}/issues/initiatives'),
            ('GET', f'{API_PREFIX}/issues/initiatives/{{id:path}}'),
            ('GET', f'{API_PREFIX}/issues/ready'),
            ('GET', f'{API_PREFIX}/issues/{{id}}'),
        }
    ),
    # What `slate claims hold <record>`, `extend` and `release` send, for a job holding a record
    # while an agent carries it. The handlers let a scoped client act only under its own client id
    # and release only a claim that id holds.
    'slate.claims.hold': frozenset(
        {
            ('POST', f'{API_PREFIX}/claims'),
            ('POST', f'{API_PREFIX}/claims/{{record}}/extend'),
            ('POST', f'{API_PREFIX}/claims/{{record}}/release'),
        }
    ),
    # What `slate issues add-comment` and `complete` send, for a job closing an issue whose fix
    # merged. The update handler narrows a body naming `status` or `closed_by` to the close of an
    # open issue.
    ISSUES_CLOSE: frozenset(
        {
            ('POST', f'{API_PREFIX}/issues/{{id}}/comments'),
            ('PATCH', f'{API_PREFIX}/issues/{{id}}'),
        }
    ),
    # What `slate issues edit --priority` and `--initiative`, `reorder`, `create`, and
    # `initiatives create`, `edit`, `complete` and `drop` send, for a job ordering the queue.
    # `create` reads the vocabulary before it files. The handlers narrow any other update to an
    # issue's priority and initiative, and a filing to triage under no parent. Nothing here deletes.
    ISSUES_PLAN: frozenset(
        {
            ('GET', f'{API_PREFIX}/issues/vocabulary'),
            ('POST', f'{API_PREFIX}/issues'),
            ('PATCH', f'{API_PREFIX}/issues/{{id}}'),
            ('POST', f'{API_PREFIX}/issues/{{id}}/rank'),
            ('POST', f'{API_PREFIX}/issues/initiatives'),
            ('PATCH', f'{API_PREFIX}/issues/initiatives/{{id:path}}'),
        }
    ),
}


def permits(scopes: frozenset[str], method: str, template: str | None) -> bool:
    return any((method, template) in SCOPE_ROUTES.get(scope, frozenset()) for scope in scopes)


def reachable(scopes: frozenset[str]) -> str:
    """What `scopes` reach, so a refused client reads what it may call from the table that refused it."""
    reached = chain.from_iterable(SCOPE_ROUTES.get(scope, frozenset()) for scope in scopes)
    routes = sorted({f'{method} {template}' for method, template in reached})
    return f'they reach only {", ".join(routes)}' if routes else 'they reach no route'
