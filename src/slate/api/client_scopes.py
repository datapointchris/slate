"""The routes each client scope reaches, as (method, route template) pairs.

A `ScopedClient` reaches a route only when one of its scopes lists it here, and every other route
answers it 403. A template is FastAPI's with every router prefix included, so
`/api/v1/issues/{id}` covers every issue.
"""

from itertools import chain

from slate.api import API_PREFIX

# The issue update reads these to decide how far it narrows a scoped client's body.
ISSUES_CLOSE = 'slate.issues.close'
RECORDS_PLAN = 'slate.records.plan'
RECORDS_RECHECK = 'slate.records.recheck'
# The route whose body `SCOPED_UPDATES` in `endpoints/issues.py` narrows for each scope reaching it.
ISSUE_UPDATE = ('PATCH', f'{API_PREFIX}/issues/{{id}}')
# The worklog join reads this one to let a scoped client into a worklog it did not open.
WORKLOGS_OBSERVE = 'slate.worklogs.observe'

SCOPE_ROUTES: dict[str, frozenset[tuple[str, str]]] = {
    # Every read of a record of any kind, of who holds which, of the documents, comments and checks
    # on it, of the order records come due for a check, of the work's totals, of the focus and each
    # pursuit's standing against it, and of the values each flag accepts. An issue in a series often
    # has no description of its own, so a reader judging it needs its initiative's.
    'slate.records.read': frozenset(
        {
            ('GET', f'{API_PREFIX}/records'),
            ('GET', f'{API_PREFIX}/records/{{number}}'),
            ('GET', f'{API_PREFIX}/records/{{number}}/changes'),
            ('GET', f'{API_PREFIX}/records/{{number}}/checks'),
            ('GET', f'{API_PREFIX}/checks/due'),
            ('GET', f'{API_PREFIX}/claims'),
            ('GET', f'{API_PREFIX}/comments'),
            ('GET', f'{API_PREFIX}/pursuits'),
            ('GET', f'{API_PREFIX}/pursuits/{{ref:path}}'),
            ('GET', f'{API_PREFIX}/documents'),
            ('GET', f'{API_PREFIX}/documents/{{record}}/{{name}}'),
            ('GET', f'{API_PREFIX}/focus'),
            ('GET', f'{API_PREFIX}/focus/history'),
            ('GET', f'{API_PREFIX}/ideas'),
            ('GET', f'{API_PREFIX}/ideas/{{id}}'),
            ('GET', f'{API_PREFIX}/initiatives'),
            ('GET', f'{API_PREFIX}/initiatives/{{id:path}}'),
            ('GET', f'{API_PREFIX}/initiatives/board'),
            ('GET', f'{API_PREFIX}/initiatives/board/text'),
            ('GET', f'{API_PREFIX}/initiatives/progress'),
            ('GET', f'{API_PREFIX}/issues'),
            ('GET', f'{API_PREFIX}/issues/{{id}}'),
            ('GET', f'{API_PREFIX}/queue'),
            ('GET', f'{API_PREFIX}/repos/board'),
            ('GET', f'{API_PREFIX}/sessions/{{id}}'),
            ('GET', f'{API_PREFIX}/tags'),
            ('GET', f'{API_PREFIX}/tags/{{name}}'),
            ('GET', f'{API_PREFIX}/totals'),
            ('GET', f'{API_PREFIX}/vocabulary'),
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
    # Joining a session to a live worklog another client opened, for a job whose session commits
    # nothing, such as a pull request's reviewer. A join stamps a session's commits onto the
    # worklog, so this scope is for a session with none. Its tokens then land on the work it served.
    WORKLOGS_OBSERVE: frozenset({('POST', f'{API_PREFIX}/worklogs/{{name}}/join')}),
    # Reporting the tokens an agent session spent, for a job whose agent reports its own usage. A
    # report names a session and carries no actor, so where it changes the session, the audit row
    # names the client.
    'slate.sessions.write': frozenset({('PUT', f'{API_PREFIX}/sessions/{{id}}/usage')}),
    # Commenting on a record or a line of its document, for a job saying what its agent did, such
    # as the fix that merged before it closes the issue. A scoped client's comment is written as
    # its own client id.
    'slate.comments.write': frozenset({('POST', f'{API_PREFIX}/comments')}),
    # Writing a document's next version, for a job writing research or a plan on the record it
    # serves. Each write adds a version, so nothing a service writes overwrites what a person wrote,
    # and the version is written as its own client id.
    'slate.documents.write': frozenset({('PUT', f'{API_PREFIX}/documents/{{record}}/{{name}}')}),
    # Setting and ending the focus, for a job applying a change of direction a person asked for.
    # A set ends the focus in force and writes the client id as who set the new one.
    'slate.focus.write': frozenset({('PUT', f'{API_PREFIX}/focus'), ('POST', f'{API_PREFIX}/focus/end')}),
    # Marking an issue or an initiative to be checked next, and withdrawing the mark, for a job
    # ordering the work that doubts an item still holds. A mark only moves the record up the due
    # order, so it writes nothing a person reads as the record's meaning.
    'slate.checks.request': frozenset(
        {
            ('PUT', f'{API_PREFIX}/records/{{number}}/check-request'),
            ('DELETE', f'{API_PREFIX}/records/{{number}}/check-request'),
        }
    ),
    # Closing an issue, for a job closing an issue whose fix merged. The update handler narrows the
    # body to the close of an issue not yet closed.
    ISSUES_CLOSE: frozenset({ISSUE_UPDATE}),
    # Accepting an incoming issue, completing or canceling one not yet closed with its reason or the
    # issue it duplicates, rewriting an issue's description, and adding a check, for a job reading
    # each record against the world. The update handler narrows the body to one of those three issue
    # changes, each alone. A check is signed by the client id.
    RECORDS_RECHECK: frozenset({ISSUE_UPDATE, ('POST', f'{API_PREFIX}/records/{{number}}/checks')}),
    # Filing an issue, setting the issue fields `SCOPED_PLAN_FIELDS` in `endpoints/issues.py` names,
    # adding and removing what an issue or an initiative waits on, creating and updating an
    # initiative, and moving an issue, initiative or idea in the queue, for a job ordering and
    # specifying the work. A filing reads the vocabulary first, and an initiative placed in a pursuit
    # by name reads the pursuit for its number. The handlers narrow any other issue update to those
    # fields, and a filing to incoming under no parent. An issue's description stays out, because a
    # person and a worker read it as the issue's meaning. An initiative's update is not narrowed, so
    # this completes, drops and describes one. Nothing here deletes a record.
    RECORDS_PLAN: frozenset(
        {
            ('GET', f'{API_PREFIX}/vocabulary'),
            ('GET', f'{API_PREFIX}/pursuits/{{ref:path}}'),
            ('POST', f'{API_PREFIX}/issues'),
            ISSUE_UPDATE,
            ('POST', f'{API_PREFIX}/issues/{{id}}/rank'),
            ('POST', f'{API_PREFIX}/issues/{{id}}/dependencies'),
            ('DELETE', f'{API_PREFIX}/issues/{{id}}/dependencies/{{dep_id}}'),
            ('POST', f'{API_PREFIX}/initiatives'),
            ('PATCH', f'{API_PREFIX}/initiatives/{{id:path}}'),
            ('POST', f'{API_PREFIX}/initiatives/{{id:path}}/rank'),
            ('POST', f'{API_PREFIX}/records/{{ref:path}}/dependencies'),
            ('DELETE', f'{API_PREFIX}/records/{{ref:path}}/dependencies/{{dep_id}}'),
            ('POST', f'{API_PREFIX}/ideas/{{id}}/rank'),
        }
    ),
    # Filing an idea with its refs, for a job keeping a capture as an idea. It reaches no edit,
    # promote, fold, rank or delete, so what becomes of the idea stays a person's call.
    'slate.ideas.write': frozenset({('POST', f'{API_PREFIX}/ideas')}),
    # Linking refs to an existing issue, initiative or idea, for a job recording what a record came
    # from, such as the inbox row that triage tracked behind it. The handlers refuse a scoped client
    # naming any linker but itself.
    'slate.refs.write': frozenset(
        {
            ('POST', f'{API_PREFIX}/issues/{{id}}/refs'),
            ('POST', f'{API_PREFIX}/initiatives/{{id:path}}/refs'),
            ('POST', f'{API_PREFIX}/ideas/{{id}}/refs'),
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
