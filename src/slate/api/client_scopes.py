"""The routes each client scope reaches, as (method, route template) pairs.

A `ScopedClient` reaches a route only when one of its scopes lists it here, and every other route
answers it 403. A template is FastAPI's with every router prefix included, so
`/api/v1/issues/{id}` covers every issue.
"""

from itertools import chain

from slate.api import API_PREFIX

# The issue update reads these two to decide how far it narrows a scoped client's body.
ISSUES_CLOSE = 'slate.issues.close'
RECORDS_PLAN = 'slate.records.plan'

SCOPE_ROUTES: dict[str, frozenset[tuple[str, str]]] = {
    # Every read of a record of any kind, and of who holds which. An issue in a series often has no
    # description of its own, so a reader judging it needs its initiative's.
    'slate.records.read': frozenset(
        {
            ('GET', f'{API_PREFIX}/records'),
            ('GET', f'{API_PREFIX}/records/{{number}}'),
            ('GET', f'{API_PREFIX}/claims'),
            ('GET', f'{API_PREFIX}/ideas'),
            ('GET', f'{API_PREFIX}/ideas/{{id}}'),
            ('GET', f'{API_PREFIX}/initiatives'),
            ('GET', f'{API_PREFIX}/initiatives/{{id:path}}'),
            ('GET', f'{API_PREFIX}/issues'),
            ('GET', f'{API_PREFIX}/issues/ready'),
            ('GET', f'{API_PREFIX}/issues/{{id}}'),
            ('GET', f'{API_PREFIX}/sessions/{{id}}'),
            ('GET', f'{API_PREFIX}/worklogs'),
            ('GET', f'{API_PREFIX}/worklogs/current'),
            ('GET', f'{API_PREFIX}/worklogs/{{name}}'),
        }
    ),
    # Holding, extending, waiting on and releasing a claim, for a job holding a record while an
    # agent carries it. A hold opens the claim's worklog. The handlers let a scoped client act only
    # under its own client id and release only a claim that id holds.
    'slate.claims.hold': frozenset(
        {
            ('POST', f'{API_PREFIX}/claims'),
            ('POST', f'{API_PREFIX}/claims/next'),
            ('POST', f'{API_PREFIX}/claims/{{record}}/extend'),
            ('POST', f'{API_PREFIX}/claims/{{record}}/wait'),
            ('POST', f'{API_PREFIX}/claims/{{record}}/resume'),
            ('POST', f'{API_PREFIX}/claims/{{record}}/release'),
        }
    ),
    # Starting, joining, linking, editing and finishing a worklog, for a job recording what its
    # agent did: the pull request it opened, the commit that landed. The handlers let a scoped client
    # act only under its own client id and finish only a worklog it opened.
    'slate.worklogs.write': frozenset(
        {
            ('POST', f'{API_PREFIX}/worklogs'),
            ('PATCH', f'{API_PREFIX}/worklogs/{{name}}'),
            ('POST', f'{API_PREFIX}/worklogs/{{name}}/join'),
            ('POST', f'{API_PREFIX}/worklogs/{{name}}/links'),
            ('POST', f'{API_PREFIX}/worklogs/{{name}}/finish'),
        }
    ),
    # Reporting the tokens an agent session spent, for a job whose agent reports its own usage when
    # it ends. A report names a session and carries no actor, so its audit row names the client.
    'slate.sessions.write': frozenset({('PUT', f'{API_PREFIX}/sessions/{{id}}/usage')}),
    # Commenting on an issue and closing it, for a job closing an issue whose fix merged. The
    # update handler narrows a body naming `status` or `closed_by` to the close of an open issue.
    ISSUES_CLOSE: frozenset(
        {
            ('POST', f'{API_PREFIX}/issues/{{id}}/comments'),
            ('PATCH', f'{API_PREFIX}/issues/{{id}}'),
        }
    ),
    # Filing an issue, setting its priority, initiative and rank, and creating and updating an
    # initiative, for a job ordering the queue. A filing reads the vocabulary first. The handlers
    # narrow any other update to an issue's priority and initiative, and a filing to triage under
    # no parent. Nothing here deletes.
    RECORDS_PLAN: frozenset(
        {
            ('GET', f'{API_PREFIX}/vocabulary'),
            ('POST', f'{API_PREFIX}/issues'),
            ('PATCH', f'{API_PREFIX}/issues/{{id}}'),
            ('POST', f'{API_PREFIX}/issues/{{id}}/rank'),
            ('POST', f'{API_PREFIX}/initiatives'),
            ('PATCH', f'{API_PREFIX}/initiatives/{{id:path}}'),
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
