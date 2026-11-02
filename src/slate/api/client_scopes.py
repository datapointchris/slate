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
    # Every read of a record of any kind, of who holds which, of the documents and comments on it,
    # and of the work's totals. An issue in a series often has no description of its own, so a
    # reader judging it needs its initiative's.
    'slate.records.read': frozenset(
        {
            ('GET', f'{API_PREFIX}/records'),
            ('GET', f'{API_PREFIX}/records/{{number}}'),
            ('GET', f'{API_PREFIX}/claims'),
            ('GET', f'{API_PREFIX}/comments'),
            ('GET', f'{API_PREFIX}/documents'),
            ('GET', f'{API_PREFIX}/documents/{{record}}/{{name}}'),
            ('GET', f'{API_PREFIX}/ideas'),
            ('GET', f'{API_PREFIX}/ideas/{{id}}'),
            ('GET', f'{API_PREFIX}/initiatives'),
            ('GET', f'{API_PREFIX}/initiatives/{{id:path}}'),
            ('GET', f'{API_PREFIX}/initiatives/{{id:path}}/progress'),
            ('GET', f'{API_PREFIX}/issues'),
            ('GET', f'{API_PREFIX}/issues/{{id}}'),
            ('GET', f'{API_PREFIX}/queue'),
            ('GET', f'{API_PREFIX}/sessions/{{id}}'),
            ('GET', f'{API_PREFIX}/totals'),
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
    # Reporting the tokens an agent session spent, for a job whose agent reports its own usage. A
    # report names a session and carries no actor, so where it changes the session, the audit row
    # names the client.
    'slate.sessions.write': frozenset({('PUT', f'{API_PREFIX}/sessions/{{id}}/usage')}),
    # Commenting on a record or a line of its document, for a job saying what its agent did, such
    # as the fix that merged before it closes the issue. A scoped client's comment is written as
    # its own client id.
    'slate.comments.write': frozenset({('POST', f'{API_PREFIX}/comments')}),
    # Closing an issue, for a job closing an issue whose fix merged. The update handler narrows a
    # body naming `status` or `closed_by` to the close of an open issue.
    ISSUES_CLOSE: frozenset({('PATCH', f'{API_PREFIX}/issues/{{id}}')}),
    # Filing an issue, setting its priority and initiative, creating and updating an initiative,
    # and moving an issue, initiative or idea in the queue, for a job ordering the queue. A filing
    # reads the vocabulary first. The handlers narrow any other update to an issue's priority and
    # initiative, and a filing to triage under no parent. Nothing here deletes.
    RECORDS_PLAN: frozenset(
        {
            ('GET', f'{API_PREFIX}/vocabulary'),
            ('POST', f'{API_PREFIX}/issues'),
            ('PATCH', f'{API_PREFIX}/issues/{{id}}'),
            ('POST', f'{API_PREFIX}/issues/{{id}}/rank'),
            ('POST', f'{API_PREFIX}/initiatives'),
            ('PATCH', f'{API_PREFIX}/initiatives/{{id:path}}'),
            ('POST', f'{API_PREFIX}/initiatives/{{id:path}}/rank'),
            ('POST', f'{API_PREFIX}/ideas/{{id}}/rank'),
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
