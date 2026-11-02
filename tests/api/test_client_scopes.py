"""The routes a scoped client's token reaches, through the real app and the real dependency chain.

`get_oidc_identity` is overridden to return the caller, so these cover the gate. The token itself is
covered in `test_oidc_auth.py`.
"""

import datetime as dt
import json
from collections.abc import Iterator
from pathlib import Path
from types import SimpleNamespace

import pytest
from fastapi import status
from sqlalchemy import select

from slate import models
from slate.api import API_PREFIX
from slate.api.client_scopes import SCOPE_ROUTES
from slate.api.exceptions import Refusal
from slate.api.oidc_auth import OIDCIdentity
from slate.api.oidc_auth import ScopedClient
from slate.api.oidc_auth import get_oidc_identity
from tests.util import show_status_and_response

READ = 'slate.records.read'
CLAIM = 'slate.claims.hold'
CLOSE = 'slate.issues.close'
PLAN = 'slate.records.plan'
WORKLOGS = 'slate.worklogs.write'
CLOSER = 'worker/close-job@service-box'
SERVICE = 'slate-svc-worker'
PERSON = OIDCIdentity(subject='person-subject', client_id='slate-cli-laptop')
SCOPES_DOCUMENT = Path(__file__).parent / 'testdata' / 'client-scopes.json'

# One request per read any scope lists, keyed by its template. Search names its words, and show
# names the issue by number, as the CLI sends them.
SCOPED_URLS = {
    f'{API_PREFIX}/records': '/records?search=scheduler',
    f'{API_PREFIX}/records/{{number}}': '/records/{issue}',
    f'{API_PREFIX}/claims': f'/claims?holder={SERVICE}',
    f'{API_PREFIX}/ideas': '/ideas',
    f'{API_PREFIX}/ideas/{{id}}': '/ideas/{idea}',
    f'{API_PREFIX}/issues': '/issues?status=all',
    f'{API_PREFIX}/issues/ready': '/issues/ready',
    f'{API_PREFIX}/vocabulary': '/vocabulary',
    f'{API_PREFIX}/issues/{{id}}': '/issues/{issue}',
    f'{API_PREFIX}/initiatives': '/initiatives',
    f'{API_PREFIX}/initiatives/{{id:path}}': '/initiatives/{initiative}',
    f'{API_PREFIX}/worklogs': '/worklogs?state=all',
    f'{API_PREFIX}/worklogs/current': '/worklogs/current?session=run-pod-session',
    f'{API_PREFIX}/worklogs/{{name}}': '/worklogs/{worklog}',
}

# Each write a scope lists, as the CLI sends it: the claim holding the issue for the fix agent, the
# hold of the queue's head, the extend, wait, resume and release of the claim the fixture seeds, each
# write on the worklog the fixture seeds, the comment saying what landed, the update that completes
# the issue and names the job closing it, and each edit a planning job makes to the queue.
SCOPED_WRITES = {
    (CLAIM, 'POST', f'{API_PREFIX}/claims'): ('/claims', {'record': '{issue}', 'holder': SERVICE, 'minutes': 60}, status.HTTP_200_OK),
    (CLAIM, 'POST', f'{API_PREFIX}/claims/next'): ('/claims/next', {'holder': SERVICE}, status.HTTP_200_OK),
    (CLAIM, 'POST', f'{API_PREFIX}/claims/{{record}}/extend'): ('/claims/{neighbor}/extend', {'holder': SERVICE}, status.HTTP_200_OK),
    (CLAIM, 'POST', f'{API_PREFIX}/claims/{{record}}/wait'): (
        '/claims/{neighbor}/wait',
        {'holder': SERVICE, 'waiting_on': 'the review round'},
        status.HTTP_200_OK,
    ),
    (CLAIM, 'POST', f'{API_PREFIX}/claims/{{record}}/resume'): ('/claims/{neighbor}/resume', {'holder': SERVICE}, status.HTTP_200_OK),
    (WORKLOGS, 'POST', f'{API_PREFIX}/worklogs'): ('/worklogs', {'record': '{issue}', 'opened_by': SERVICE}, status.HTTP_201_CREATED),
    (WORKLOGS, 'PATCH', f'{API_PREFIX}/worklogs/{{name}}'): ('/worklogs/{worklog}', {'workflow': 'oneshot'}, status.HTTP_200_OK),
    (WORKLOGS, 'POST', f'{API_PREFIX}/worklogs/{{name}}/join'): (
        '/worklogs/{worklog}/join',
        {'session': 'run-pod-session'},
        status.HTTP_200_OK,
    ),
    (WORKLOGS, 'POST', f'{API_PREFIX}/worklogs/{{name}}/links'): (
        '/worklogs/{worklog}/links',
        {'kind': 'pull_request', 'ref': 'https://github.com/example/repo/pull/3', 'linked_by': SERVICE},
        status.HTTP_200_OK,
    ),
    (WORKLOGS, 'POST', f'{API_PREFIX}/worklogs/{{name}}/finish'): (
        '/worklogs/{worklog}/finish',
        {'finished_by': SERVICE},
        status.HTTP_200_OK,
    ),
    (CLAIM, 'POST', f'{API_PREFIX}/claims/{{record}}/release'): (
        '/claims/{neighbor}/release',
        {'released_by': SERVICE},
        status.HTTP_200_OK,
    ),
    (CLOSE, 'POST', f'{API_PREFIX}/issues/{{id}}/comments'): (
        '/issues/{issue}/comments',
        {'body': 'Landed: a merged fix'},
        status.HTTP_201_CREATED,
    ),
    (CLOSE, 'PATCH', f'{API_PREFIX}/issues/{{id}}'): ('/issues/{issue}', {'status': 'completed', 'closed_by': CLOSER}, status.HTTP_200_OK),
    (PLAN, 'PATCH', f'{API_PREFIX}/issues/{{id}}'): ('/issues/{issue}', {'priority': 2, 'initiative': '{initiative}'}, status.HTTP_200_OK),
    (PLAN, 'POST', f'{API_PREFIX}/issues/{{id}}/rank'): ('/issues/{issue}/rank', {'after': '{neighbor}'}, status.HTTP_200_OK),
    (PLAN, 'POST', f'{API_PREFIX}/issues'): (
        '/issues',
        {'title': 'left of the initiative', 'initiative': '{initiative}', 'status': 'triage'},
        status.HTTP_201_CREATED,
    ),
    (PLAN, 'POST', f'{API_PREFIX}/initiatives'): (
        '/initiatives',
        {'name': 'grouped by a planning job'},
        status.HTTP_201_CREATED,
    ),
    (PLAN, 'PATCH', f'{API_PREFIX}/initiatives/{{id:path}}'): (
        '/initiatives/{initiative}',
        {'status': 'dropped', 'status_reason': 'merged into another'},
        status.HTTP_200_OK,
    ),
}


def scoped_routes() -> Iterator[tuple[str, str, str]]:
    for scope, routes in SCOPE_ROUTES.items():
        for method, template in routes:
            yield scope, method, template


SCOPED_ROUTES = sorted(scoped_routes())


def scoped(scope: str) -> ScopedClient:
    return ScopedClient(client_id=SERVICE, scopes=frozenset({scope}))


@pytest.fixture
def as_caller(txn_api):
    """Seed two issues of one priority, one initiative, one idea, the service's claim on the neighbor, and a worklog it opened.

    `caller(identity)` sets who the token says is calling and returns the client, and `url(template)`
    fills a template with the seeded rows. The initiative is named, as the CLI names one.
    """
    client, session = txn_api
    issue = models.Issue(title='scheduler issue', rank=1.0)
    neighbor = models.Issue(title='scheduler neighbor', rank=2.0)
    initiative = models.Initiative(name='scheduler-initiative')
    idea = models.Idea(title='scheduler idea')
    session.add_all([issue, neighbor, initiative, idea])
    session.flush()
    now = dt.datetime.now(dt.UTC)
    session.add(models.Claim(record_number=neighbor.number, holder=SERVICE, started_ts=now, expires_ts=now + dt.timedelta(hours=1)))
    session.add(models.Worklog(record_number=issue.number, sequence=1, opened_by=SERVICE, started_ts=now))
    session.flush()

    def caller(identity):
        client.app.dependency_overrides[get_oidc_identity] = lambda: identity
        return client

    def url(template):
        return template.format(
            issue=issue.number, neighbor=neighbor.number, initiative=initiative.name, idea=idea.number, worklog=f'{issue.number}.1'
        )

    return SimpleNamespace(caller=caller, url=url, issue=issue, session=session)


@pytest.mark.parametrize(('scope', 'method', 'template'), SCOPED_ROUTES)
def test_a_scoped_client_reaches_every_route_its_scope_lists(as_caller, scope, method, template):
    if method == 'GET':
        url, body, expected = SCOPED_URLS[template], None, status.HTTP_200_OK
    else:
        url, body, expected = SCOPED_WRITES[(scope, method, template)]
        body = body and {key: as_caller.url(value) if isinstance(value, str) else value for key, value in body.items()}
    response = as_caller.caller(scoped(scope)).request(method, as_caller.url(url), json=body)
    assert response.status_code == expected, show_status_and_response(response)


@pytest.mark.parametrize(
    ('scope', 'method', 'url'),
    [
        (READ, 'POST', '/issues'),
        (READ, 'PATCH', '/issues/{issue}'),
        (READ, 'DELETE', '/issues/{issue}'),
        (READ, 'POST', '/claims'),
        (READ, 'POST', '/issues/{issue}/comments'),
        (READ, 'GET', '/issues/{issue}/comments'),
        (READ, 'GET', '/vocabulary'),
        (READ, 'GET', '/labels'),
        (READ, 'POST', '/initiatives'),
        (READ, 'PATCH', '/initiatives/{initiative}'),
        (READ, 'DELETE', '/initiatives/{initiative}'),
        (READ, 'POST', '/ideas'),
        (READ, 'POST', '/ideas/{idea}/promote'),
        (CLOSE, 'GET', '/issues/{issue}'),
        (CLOSE, 'POST', '/issues'),
        (CLOSE, 'DELETE', '/issues/{issue}'),
        (CLOSE, 'POST', '/claims'),
        (CLOSE, 'PATCH', '/initiatives/{initiative}'),
        (CLAIM, 'GET', '/claims'),
        (CLAIM, 'PATCH', '/issues/{issue}'),
        (CLAIM, 'POST', '/worklogs/{worklog}/links'),
        (PLAN, 'DELETE', '/issues/{issue}'),
        (PLAN, 'DELETE', '/initiatives/{initiative}'),
        (READ, 'POST', '/worklogs'),
        (WORKLOGS, 'POST', '/claims'),
        (WORKLOGS, 'GET', '/worklogs/{worklog}'),
    ],
    ids=[
        'read-create',
        'read-update',
        'read-delete',
        'read-hold',
        'read-comment',
        'read-unlisted-read',
        'read-vocabulary',
        'read-labels-router',
        'read-initiatives-create',
        'read-initiatives-update',
        'read-initiatives-delete',
        'read-ideas-create',
        'read-ideas-promote',
        'close-read',
        'close-create',
        'close-delete',
        'close-hold',
        'close-initiative-update',
        'hold-list',
        'hold-update',
        'hold-link',
        'plan-delete',
        'plan-initiative-delete',
        'read-worklog-start',
        'worklogs-hold',
        'worklogs-read',
    ],
)
def test_a_scoped_client_is_refused_off_its_scopes(as_caller, scope, method, url):
    response = as_caller.caller(scoped(scope)).request(method, as_caller.url(url), json={'title': 'written by a scoped client'})
    assert response.status_code == status.HTTP_403_FORBIDDEN, show_status_and_response(response)
    message = response.json()['message']
    assert Refusal.OUTSIDE_CLIENT_SCOPES in message
    assert all(f'{method} {template}' in message for method, template in SCOPE_ROUTES[scope])


ONLY_CLOSES = Refusal.SCOPED_CLIENT_ONLY_CLOSES
ONLY_PRIORITY_AND_INITIATIVE = Refusal.SCOPED_CLIENT_ONLY_SETS_PRIORITY_AND_INITIATIVE


@pytest.mark.parametrize(
    ('scopes', 'body', 'refusal', 'refused'),
    [
        ({CLOSE}, {'status': 'completed', 'title': 'renamed'}, ONLY_CLOSES, 'title'),
        ({CLOSE}, {'priority': 0}, ONLY_CLOSES, 'priority'),
        ({CLOSE}, {'status': 'triage'}, ONLY_CLOSES, 'status'),
        ({PLAN}, {'status': 'completed'}, ONLY_PRIORITY_AND_INITIATIVE, 'status'),
        ({PLAN, CLOSE}, {'title': 'renamed'}, ONLY_PRIORITY_AND_INITIATIVE, 'title'),
        ({PLAN, CLOSE}, {'status': 'completed', 'priority': 2}, ONLY_CLOSES, 'priority'),
    ],
    ids=[
        'close-an-edit-beside-a-close',
        'close-an-edit-alone',
        'close-a-status-that-keeps-it-open',
        'plan-a-close',
        'both-a-title',
        'both-an-edit-beside-a-close',
    ],
)
def test_a_scoped_update_is_a_close_or_sets_only_priority_and_initiative(as_caller, scopes, body, refusal, refused):
    client = as_caller.caller(ScopedClient(client_id=SERVICE, scopes=frozenset(scopes)))
    response = client.patch(as_caller.url('/issues/{issue}'), json=body)
    assert response.status_code == status.HTTP_403_FORBIDDEN, show_status_and_response(response)
    assert refusal in response.json()['message']
    assert refused in response.json()['message']


@pytest.mark.parametrize(
    'body',
    [{'title': 'ready on its own'}, {'title': 'holds its parent back', 'status': 'triage', 'parent': '{issue}'}],
    ids=['an-open-filing', 'a-filing-under-a-parent'],
)
def test_a_scoped_filing_goes_into_triage_under_no_parent(as_caller, body):
    body = {key: as_caller.url(value) for key, value in body.items()}
    response = as_caller.caller(scoped(PLAN)).post(as_caller.url('/issues'), json=body)
    assert response.status_code == status.HTTP_403_FORBIDDEN, show_status_and_response(response)
    assert Refusal.SCOPED_CLIENT_FILES_ONLY_INTO_TRIAGE in response.json()['message']


def test_the_close_scope_signs_with_its_client_id_and_closes_an_issue_once(as_caller):
    client = as_caller.caller(scoped(CLOSE))
    url = as_caller.url('/issues/{issue}')
    closed = client.patch(url, json={'status': 'completed'})
    assert closed.status_code == status.HTTP_200_OK, show_status_and_response(closed)
    assert closed.json()['issue']['closed_by'] == SERVICE

    moved = client.patch(url, json={'status': 'canceled'})
    assert moved.status_code == status.HTTP_403_FORBIDDEN, show_status_and_response(moved)
    assert Refusal.SCOPED_CLIENT_ONLY_CLOSES in moved.json()['message']
    assert 'completed' in moved.json()['message']


def test_the_hold_scope_holds_under_its_own_id_and_releases_only_that(as_caller):
    """A person may release whoever holds a record, so without the narrowing a job could free a session's work."""
    record = as_caller.url('{issue}')
    release = as_caller.url('/claims/{issue}/release')
    held = as_caller.caller(PERSON).post('/claims', json={'record': record, 'holder': 'claude-code/peer'})
    assert held.status_code == status.HTTP_200_OK, show_status_and_response(held)

    service = as_caller.caller(scoped(CLAIM))
    freed = service.post(release, json={'released_by': SERVICE})
    assert freed.status_code == status.HTTP_403_FORBIDDEN, show_status_and_response(freed)
    assert Refusal.SCOPED_CLIENT_RELEASES_ITS_OWN in freed.json()['message']
    assert 'claimed by claude-code/peer' in freed.json()['message']

    as_caller.caller(PERSON).post(release, json={'released_by': 'claude-code/peer'})
    service = as_caller.caller(scoped(CLAIM))
    signed = service.post('/claims', json={'record': record, 'holder': 'claude-code/peer'})
    assert signed.status_code == status.HTTP_403_FORBIDDEN, show_status_and_response(signed)
    assert Refusal.SCOPED_CLIENT_CLAIMS_AS_ITSELF in signed.json()['message']

    held = service.post('/claims', json={'record': record, 'holder': SERVICE})
    assert held.status_code == status.HTTP_200_OK, show_status_and_response(held)
    assert held.json()['claim']['holder'] == SERVICE
    released = service.post(release, json={'released_by': SERVICE})
    assert released.status_code == status.HTTP_200_OK, show_status_and_response(released)
    assert released.json()['claim']['ending'] == 'released'


@pytest.mark.parametrize(
    ('method', 'path', 'body'),
    [
        ('POST', '/finish', {'finished_by': SERVICE}),
        ('POST', '/join', {'session': 'peer-session'}),
        ('PATCH', '', {'workflow': 'oneshot'}),
    ],
    ids=['finish', 'join', 'edit'],
)
def test_the_worklogs_scope_joins_edits_and_finishes_only_a_worklog_its_client_opened(as_caller, method, path, body):
    """A job joining a session into another's worklog stamps that session's commits onto it, and finishing one ends its spans."""
    opened = as_caller.caller(PERSON).post('/worklogs', json={'record': as_caller.url('{issue}'), 'opened_by': 'claude-code/peer'})
    assert opened.status_code == status.HTTP_201_CREATED, show_status_and_response(opened)

    url = f'/worklogs/{opened.json()["worklog"]["name"]}{path}'
    refused = as_caller.caller(scoped(WORKLOGS)).request(method, url, json=body)
    assert refused.status_code == status.HTTP_403_FORBIDDEN, show_status_and_response(refused)
    assert Refusal.SCOPED_CLIENT_WRITES_ITS_OWN_WORKLOGS in refused.json()['message']
    assert 'claude-code/peer' in refused.json()['message']


def test_a_scope_the_table_does_not_name_reaches_nothing(as_caller):
    client = as_caller.caller(ScopedClient(client_id=SERVICE, scopes=frozenset({'slate.unknown'})))
    response = client.get('/issues')
    assert response.status_code == status.HTTP_403_FORBIDDEN, show_status_and_response(response)


def test_a_person_reaches_a_route_no_scope_lists(as_caller):
    response = as_caller.caller(PERSON).post('/labels', json={'slug': 'written-by-a-person'})
    assert response.status_code == status.HTTP_201_CREATED, show_status_and_response(response)


def test_the_audit_trail_names_who_made_each_edit(as_caller):
    edited = as_caller.caller(PERSON).patch(as_caller.url('/issues/{issue}'), json={'title': 'retitled'})
    assert edited.status_code == status.HTTP_200_OK, show_status_and_response(edited)

    service = as_caller.caller(ScopedClient(client_id=SERVICE, scopes=frozenset({PLAN, CLAIM})))
    planned = service.patch(as_caller.url('/issues/{issue}'), json={'priority': 2})
    assert planned.status_code == status.HTTP_200_OK, show_status_and_response(planned)
    claimed = service.post('/claims', json={'record': as_caller.url('{issue}'), 'holder': SERVICE, 'minutes': 60})
    assert claimed.status_code == status.HTTP_200_OK, show_status_and_response(claimed)

    trail = as_caller.session.scalars(
        select(models.RecordChange)
        .where(models.RecordChange.table_name == 'issues', models.RecordChange.record_id == str(as_caller.issue.id))
        .where(models.RecordChange.operation == 'update')
        .order_by(models.RecordChange.id)
    ).all()
    retitle, plan = trail
    assert (retitle.actor, retitle.changes['title']) == (PERSON.subject, {'old': 'scheduler issue', 'new': 'retitled'})
    assert (plan.actor, plan.changes['priority']['new']) == (SERVICE, 2)
    held = as_caller.session.scalars(
        select(models.RecordChange).where(
            models.RecordChange.table_name == 'claims', models.RecordChange.record_id == str(claimed.json()['claim']['id'])
        )
    ).one()
    assert (held.operation, held.actor, held.changes['holder']) == ('insert', SERVICE, {'new': SERVICE})


def test_no_credentials_is_401(as_caller):
    response = as_caller.caller(None).get('/issues')
    assert response.status_code == status.HTTP_401_UNAUTHORIZED, show_status_and_response(response)


def test_the_document_the_cli_reads_holds_the_scope_table():
    """The CLI's suite holds the scope it requests, and every request it sends, against this file.

    Its fake provider grants the scope the CLI spells, so a scope renamed here would otherwise pass
    both suites and answer 403 to every scheduled run. A stale file is rewritten here and the test
    fails, so the change is committed beside the table.
    """
    table = {scope: sorted(f'{method} {template}' for method, template in SCOPE_ROUTES[scope]) for scope in sorted(SCOPE_ROUTES)}
    document = json.dumps(table, indent=2) + '\n'
    if not SCOPES_DOCUMENT.exists() or SCOPES_DOCUMENT.read_text() != document:
        SCOPES_DOCUMENT.parent.mkdir(exist_ok=True)
        SCOPES_DOCUMENT.write_text(document)
        pytest.fail(f'{SCOPES_DOCUMENT} did not hold SCOPE_ROUTES and has been rewritten; commit it')
