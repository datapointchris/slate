"""The routes a scoped client's token reaches, through the real app and the real dependency chain.

`get_oidc_identity` is overridden to return the caller, so these cover the gate. The token itself is
covered in `test_oidc_auth.py`.
"""

import datetime as dt
import json
from collections.abc import Iterator
from itertools import product
from pathlib import Path
from types import SimpleNamespace
from typing import Any

import pytest
from fastapi import status
from sqlalchemy import select

from slate import models
from slate.api import API_PREFIX
from slate.api.client_scopes import ISSUE_UPDATE
from slate.api.client_scopes import SCOPE_ROUTES
from slate.api.endpoints.issues import SCOPED_UPDATES
from slate.api.exceptions import Refusal
from slate.api.oidc_auth import OIDCIdentity
from slate.api.oidc_auth import ScopedClient
from slate.api.oidc_auth import get_oidc_identity
from tests.util import show_status_and_response

READ = 'slate.records.read'
CLAIM = 'slate.claims.hold'
CHECKS = 'slate.checks.request'
CLOSE = 'slate.issues.close'
COMMENTS = 'slate.comments.write'
DOCUMENTS = 'slate.documents.write'
FOCUS = 'slate.focus.write'
PLAN = 'slate.records.plan'
RECHECK = 'slate.records.recheck'
WORKLOGS = 'slate.worklogs.write'
SESSIONS = 'slate.sessions.write'
OBSERVE = 'slate.worklogs.observe'
RUN_POD_SESSION = 'run-pod-session'
CLOSER = 'worker/close-job@service-box'
SERVICE = 'slate-svc-worker'
PERSON = OIDCIdentity(subject='person-subject', client_id='slate-cli-laptop')
SCOPES_DOCUMENT = Path(__file__).parent / 'testdata' / 'client-scopes.json'
SCOPED_BODIES_DOCUMENT = Path(__file__).parent / 'testdata' / 'scoped-bodies.json'

# One request per read any scope lists, keyed by its template. Search names its words, and show
# names the issue by number, as the CLI sends them.
SCOPED_URLS = {
    f'{API_PREFIX}/records': '/records?search=scheduler',
    f'{API_PREFIX}/records/{{number}}': '/records/{issue}',
    f'{API_PREFIX}/records/{{number}}/changes': '/records/{issue}/changes',
    f'{API_PREFIX}/records/{{number}}/checks': '/records/{issue}/checks',
    f'{API_PREFIX}/checks/due': '/checks/due?limit=5',
    f'{API_PREFIX}/claims': f'/claims?holder={SERVICE}',
    f'{API_PREFIX}/comments': '/comments?record={issue}',
    f'{API_PREFIX}/pursuits': '/pursuits',
    f'{API_PREFIX}/pursuits/{{ref:path}}': '/pursuits/{pursuit}',
    f'{API_PREFIX}/documents': '/documents?record={issue}',
    f'{API_PREFIX}/documents/{{record}}/{{name}}': '/documents/{issue}/plan',
    f'{API_PREFIX}/focus': '/focus',
    f'{API_PREFIX}/focus/history': '/focus/history',
    f'{API_PREFIX}/ideas': '/ideas',
    f'{API_PREFIX}/ideas/{{id}}': '/ideas/{idea}',
    f'{API_PREFIX}/issues': '/issues?status=all',
    f'{API_PREFIX}/queue': '/queue?step=build',
    f'{API_PREFIX}/vocabulary': '/vocabulary',
    f'{API_PREFIX}/issues/{{id}}': '/issues/{issue}',
    f'{API_PREFIX}/initiatives': '/initiatives',
    f'{API_PREFIX}/initiatives/{{id:path}}': '/initiatives/{initiative}',
    f'{API_PREFIX}/initiatives/board': '/initiatives/board',
    f'{API_PREFIX}/initiatives/board/text': '/initiatives/board/text',
    f'{API_PREFIX}/initiatives/progress': '/initiatives/progress?initiative={initiative}',
    f'{API_PREFIX}/repos/board': '/repos/board',
    f'{API_PREFIX}/sessions/{{id}}': f'/sessions/{RUN_POD_SESSION}',
    f'{API_PREFIX}/tags': '/tags',
    f'{API_PREFIX}/tags/{{name}}': '/tags/security',
    f'{API_PREFIX}/totals': '/totals?record={issue}',
    f'{API_PREFIX}/worklogs': '/worklogs?state=all',
    f'{API_PREFIX}/worklogs/current': '/worklogs/current?session=run-pod-session',
    f'{API_PREFIX}/worklogs/{{name}}': '/worklogs/{worklog}',
}

# Each write a scope lists, as the CLI sends it: the claim holding the issue for the fix agent, the
# hold of the queue's head, the extend, wait, resume and release of the claim the fixture seeds, each
# write on the worklog the fixture seeds, the comment saying what landed, the update that completes
# the issue and names the job closing it, and each edit a planning job makes to the queue.
SCOPED_WRITES: dict[tuple[str, str, str], tuple[str, dict[str, Any] | None, int]] = {
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
    (WORKLOGS, 'PATCH', f'{API_PREFIX}/worklogs/{{name}}'): ('/worklogs/{worklog}', {'step': 'build'}, status.HTTP_200_OK),
    (WORKLOGS, 'POST', f'{API_PREFIX}/worklogs/{{name}}/join'): (
        '/worklogs/{worklog}/join',
        {'session': 'run-pod-session'},
        status.HTTP_200_OK,
    ),
    (OBSERVE, 'POST', f'{API_PREFIX}/worklogs/{{name}}/join'): (
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
    (SESSIONS, 'PUT', f'{API_PREFIX}/sessions/{{id}}/usage'): (
        f'/sessions/{RUN_POD_SESSION}/usage',
        {'messages': [{'key': 'msg_1', 'written_ts': '2026-11-02T12:00:00Z', 'tokens': {'cache_read': 900, 'output': 40}}]},
        status.HTTP_200_OK,
    ),
    (CLAIM, 'POST', f'{API_PREFIX}/claims/{{record}}/release'): (
        '/claims/{neighbor}/release',
        {'released_by': SERVICE},
        status.HTTP_200_OK,
    ),
    (COMMENTS, 'POST', f'{API_PREFIX}/comments'): (
        '/comments',
        {'record': '{issue}', 'body': 'Landed: a merged fix'},
        status.HTTP_201_CREATED,
    ),
    (DOCUMENTS, 'PUT', f'{API_PREFIX}/documents/{{record}}/{{name}}'): (
        '/documents/{issue}/research',
        {'body': 'What earlier work found', 'kind': 'research'},
        status.HTTP_201_CREATED,
    ),
    (CHECKS, 'PUT', f'{API_PREFIX}/records/{{number}}/check-request'): (
        '/records/{issue}/check-request',
        {'reason': 'its paths moved since it was filed'},
        status.HTTP_200_OK,
    ),
    (CHECKS, 'DELETE', f'{API_PREFIX}/records/{{number}}/check-request'): ('/records/{issue}/check-request', None, status.HTTP_200_OK),
    (FOCUS, 'PUT', f'{API_PREFIX}/focus'): ('/focus', {'statement': 'The scheduler first', 'until_date': '2099-12-31'}, status.HTTP_200_OK),
    (FOCUS, 'POST', f'{API_PREFIX}/focus/end'): ('/focus/end', {'reason': 'The scheduler runs'}, status.HTTP_200_OK),
    (CLOSE, 'PATCH', f'{API_PREFIX}/issues/{{id}}'): ('/issues/{issue}', {'status': 'completed', 'closed_by': CLOSER}, status.HTTP_200_OK),
    (RECHECK, 'PATCH', f'{API_PREFIX}/issues/{{id}}'): (
        '/issues/{issue}',
        {'description': 'It times out on a cold start'},
        status.HTTP_200_OK,
    ),
    (RECHECK, 'POST', f'{API_PREFIX}/records/{{number}}/checks'): (
        '/records/{issue}/checks',
        {'verdict': 'holds', 'evidence': 'The scheduler still drops it', 'cites': [{'kind': 'path', 'ref': 'scheduler-repo:run.py'}]},
        status.HTTP_201_CREATED,
    ),
    (PLAN, 'PATCH', f'{API_PREFIX}/issues/{{id}}'): (
        '/issues/{issue}',
        {'priority': 2, 'initiative': '{initiative}', 'acceptance': 'It answers 200', 'paths': ['src/slate']},
        status.HTTP_200_OK,
    ),
    (PLAN, 'POST', f'{API_PREFIX}/issues/{{id}}/rank'): ('/issues/{issue}/rank', {'after': '{neighbor}'}, status.HTTP_200_OK),
    (PLAN, 'POST', f'{API_PREFIX}/issues/{{id}}/dependencies'): (
        '/issues/{issue}/dependencies',
        {'depends_on': '{neighbor}'},
        status.HTTP_201_CREATED,
    ),
    (PLAN, 'DELETE', f'{API_PREFIX}/issues/{{id}}/dependencies/{{dep_id}}'): (
        '/issues/{neighbor}/dependencies/{blocker}',
        None,
        status.HTTP_200_OK,
    ),
    (PLAN, 'POST', f'{API_PREFIX}/issues'): (
        '/issues',
        {'title': 'left of the initiative', 'initiative': '{initiative}', 'status': 'incoming'},
        status.HTTP_201_CREATED,
    ),
    (PLAN, 'POST', f'{API_PREFIX}/initiatives'): (
        '/initiatives',
        {'name': 'grouped by a planning job', 'summary': 'A planning job grouped it.'},
        status.HTTP_201_CREATED,
    ),
    (PLAN, 'PATCH', f'{API_PREFIX}/initiatives/{{id:path}}'): (
        '/initiatives/{initiative}',
        {'status': 'dropped', 'status_reason': 'merged into another'},
        status.HTTP_200_OK,
    ),
    (PLAN, 'POST', f'{API_PREFIX}/initiatives/{{id:path}}/rank'): (
        '/initiatives/{initiative}/rank',
        {'before': '{idea}'},
        status.HTTP_200_OK,
    ),
    (PLAN, 'POST', f'{API_PREFIX}/records/{{ref:path}}/dependencies'): (
        '/records/{initiative}/dependencies',
        {'depends_on': '{issue}'},
        status.HTTP_201_CREATED,
    ),
    (PLAN, 'DELETE', f'{API_PREFIX}/records/{{ref:path}}/dependencies/{{dep_id}}'): (
        '/records/{initiative}/dependencies/{blocker}',
        None,
        status.HTTP_200_OK,
    ),
    (PLAN, 'POST', f'{API_PREFIX}/ideas/{{id}}/rank'): ('/ideas/{idea}/rank', {'after': '{issue}'}, status.HTTP_200_OK),
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
    """Seed the rows each scoped route reads.

    They are two issues of one priority, an initiative, an idea, a tag, a pursuit, the service's
    claim on the neighbor, its worklog, a session, a plan and a focus in force. `caller(identity)` sets who the token says is
    calling and returns the client, and `url(template)` fills a template with the seeded rows. The
    initiative is named, as the CLI names one.
    """
    client, session = txn_api
    issue = models.Issue(title='scheduler issue', repo='scheduler-repo', rank=1.0)
    neighbor = models.Issue(title='scheduler neighbor', rank=2.0)
    blocker = models.Issue(title='what the neighbor waits on', rank=3.0)
    initiative = models.Initiative(name='scheduler-initiative', summary='The scheduler can reach it.')
    idea = models.Idea(title='scheduler idea')
    tag = models.Tag(name='security', description='What an attacker reaches')
    pursuit = models.Pursuit(name='Scheduler on k3s', summary='The scheduler runs from a released image.')
    session.add_all([issue, neighbor, blocker, initiative, idea, tag, pursuit])
    session.flush()
    session.add(models.IssueDependency(issue_id=neighbor.id, depends_on_id=blocker.id))
    session.add(models.InitiativeDependency(initiative_id=initiative.id, depends_on_number=blocker.number))
    now = dt.datetime.now(dt.UTC)
    session.add(models.Claim(record_number=neighbor.number, holder=SERVICE, started_ts=now, expires_ts=now + dt.timedelta(hours=1)))
    session.add(models.Worklog(record_number=issue.number, sequence=1, opened_by=SERVICE, started_ts=now))
    session.add(models.AgentSession(id=RUN_POD_SESSION, host='run-pod'))
    plan = models.Document(record_number=issue.number, name='plan', kind='plan')
    session.add(models.DocumentVersion(document=plan, version=1, body='1. Fix it', written_by=PERSON.subject))
    session.add(models.Focus(statement='The scheduler first', until_date=dt.date(2099, 12, 31), set_by=PERSON.subject))
    session.flush()

    def caller(identity):
        client.app.dependency_overrides[get_oidc_identity] = lambda: identity
        return client

    def url(template):
        return template.format(
            issue=issue.number,
            neighbor=neighbor.number,
            blocker=blocker.number,
            initiative=initiative.name,
            idea=idea.number,
            pursuit=pursuit.name,
            worklog=f'{issue.number}.1',
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
        (READ, 'POST', '/comments'),
        (READ, 'PUT', '/documents/{issue}/plan'),
        (READ, 'PUT', '/focus'),
        (READ, 'POST', '/focus/end'),
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
        (CLOSE, 'POST', '/comments'),
        (COMMENTS, 'DELETE', '/comments/1'),
        (COMMENTS, 'PUT', '/documents/{issue}/plan'),
        (FOCUS, 'GET', '/focus'),
        (FOCUS, 'PUT', '/documents/{issue}/plan'),
        (CLAIM, 'GET', '/claims'),
        (CLAIM, 'PATCH', '/issues/{issue}'),
        (CLAIM, 'POST', '/worklogs/{worklog}/links'),
        (PLAN, 'DELETE', '/issues/{issue}'),
        (PLAN, 'DELETE', '/initiatives/{initiative}'),
        (READ, 'POST', '/worklogs'),
        (WORKLOGS, 'POST', '/claims'),
        (WORKLOGS, 'GET', '/worklogs/{worklog}'),
        (OBSERVE, 'POST', '/worklogs'),
        (OBSERVE, 'POST', '/worklogs/{worklog}/finish'),
        (OBSERVE, 'POST', '/worklogs/{worklog}/links'),
        (OBSERVE, 'PATCH', '/worklogs/{worklog}'),
    ],
    ids=[
        'read-create',
        'read-update',
        'read-delete',
        'read-hold',
        'read-comment',
        'read-document-write',
        'read-focus-set',
        'read-focus-end',
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
        'close-comment',
        'comments-delete',
        'comments-document-write',
        'focus-read',
        'focus-document-write',
        'hold-list',
        'hold-update',
        'hold-link',
        'plan-delete',
        'plan-initiative-delete',
        'read-worklog-start',
        'worklogs-hold',
        'worklogs-read',
        'observe-start',
        'observe-finish',
        'observe-link',
        'observe-edit',
    ],
)
def test_a_scoped_client_is_refused_off_its_scopes(as_caller, scope, method, url):
    response = as_caller.caller(scoped(scope)).request(method, as_caller.url(url), json={'title': 'written by a scoped client'})
    assert response.status_code == status.HTTP_403_FORBIDDEN, show_status_and_response(response)
    message = response.json()['message']
    assert Refusal.OUTSIDE_CLIENT_SCOPES in message
    assert all(f'{method} {template}' in message for method, template in SCOPE_ROUTES[scope])


@pytest.mark.parametrize(
    ('scopes', 'body', 'refused'),
    [
        ({CLOSE}, {'status': 'completed', 'title': 'renamed'}, 'title'),
        ({CLOSE}, {'priority': 0}, 'priority'),
        ({CLOSE}, {'status': 'incoming'}, 'status'),
        ({PLAN, CLOSE, RECHECK}, {'priority': 2, 'description': 'rewritten'}, None),
        ({PLAN, CLOSE}, {'status': 'completed', 'priority': 2}, None),
    ],
    ids=[
        'close-an-edit-beside-a-close',
        'close-an-edit-alone',
        'close-a-status-that-keeps-it-open',
        'every-write-scope-a-plan-field-beside-a-description',
        'both-an-edit-beside-a-close',
    ],
)
def test_a_scoped_update_passes_only_whole_under_one_held_scope(as_caller, scopes, body, refused):
    client = as_caller.caller(ScopedClient(client_id=SERVICE, scopes=frozenset(scopes)))
    response = client.patch(as_caller.url('/issues/{issue}'), json=body)
    assert response.status_code == status.HTTP_403_FORBIDDEN, show_status_and_response(response)
    if refused is not None:
        assert refused in response.json()['message']


@pytest.mark.parametrize(
    'body',
    [{'title': 'ready on its own'}, {'title': 'holds its parent back', 'status': 'incoming', 'parent': '{issue}'}],
    ids=['an-open-filing', 'a-filing-under-a-parent'],
)
def test_a_scoped_filing_is_incoming_under_no_parent(as_caller, body):
    body = {key: as_caller.url(value) for key, value in body.items()}
    response = as_caller.caller(scoped(PLAN)).post(as_caller.url('/issues'), json=body)
    assert response.status_code == status.HTTP_403_FORBIDDEN, show_status_and_response(response)
    assert Refusal.SCOPED_CLIENT_FILES_ONLY_AS_INCOMING in response.json()['message']


def test_the_close_scope_signs_with_its_client_id_and_closes_an_issue_once(as_caller):
    client = as_caller.caller(scoped(CLOSE))
    url = as_caller.url('/issues/{issue}')
    closed = client.patch(url, json={'status': 'completed'})
    assert closed.status_code == status.HTTP_200_OK, show_status_and_response(closed)
    assert closed.json()['issue']['closed_by'] == SERVICE

    moved = client.patch(url, json={'status': 'canceled'})
    assert moved.status_code == status.HTTP_403_FORBIDDEN, show_status_and_response(moved)
    assert 'completed' in moved.json()['message']


def test_the_recheck_scope_accepts_closes_and_describes_what_the_plan_scope_is_refused(as_caller):
    """A recheck places an incoming filing, closes what is done or duplicated, corrects a description, and checks each."""

    def filed(title, **fields):
        response = as_caller.caller(PERSON).post('/issues', json={'title': title, **fields})
        assert response.status_code == status.HTTP_201_CREATED, show_status_and_response(response)
        return response.json()['issue']['number']

    def shown(issue):
        return issue['status'], issue['description'], issue['closed_by'], (issue['duplicate_of'] or {}).get('number')

    duplicated = as_caller.issue.number
    rewritten = 'It gives up after one retry'
    changes = [
        (filed('filed by a hook', status='incoming'), {'status': 'open'}, 'holds', ('open', None, None, None)),
        (filed('shipped last week'), {'status': 'completed', 'closed_by': CLOSER}, 'done', ('completed', None, CLOSER, None)),
        (
            filed('the scheduler issue again'),
            {'status': 'canceled', 'duplicate_of': duplicated},
            'duplicate',
            ('canceled', None, SERVICE, duplicated),
        ),
        (filed('the exporter', description='It times out'), {'description': rewritten}, 'corrected', ('open', rewritten, None, None)),
    ]
    planner = as_caller.caller(scoped(PLAN))
    for number, body, _, _ in changes:
        refused = planner.patch(f'/issues/{number}', json=body)
        assert refused.status_code == status.HTTP_403_FORBIDDEN, show_status_and_response(refused)

    rechecker = as_caller.caller(scoped(RECHECK))
    for number, body, verdict, reached in changes:
        written = rechecker.patch(f'/issues/{number}', json=body)
        assert written.status_code == status.HTTP_200_OK, show_status_and_response(written)
        assert shown(written.json()['issue']) == reached
        checked = rechecker.post(f'/records/{number}/checks', json={'verdict': verdict, 'evidence': 'Read against the scheduler'})
        assert checked.status_code == status.HTTP_201_CREATED, show_status_and_response(checked)
        assert checked.json()['check']['checked_by'] == SERVICE

    accepted = changes[0][0]
    again = rechecker.patch(f'/issues/{accepted}', json={'status': 'open'})
    assert again.status_code == status.HTTP_403_FORBIDDEN, show_status_and_response(again)
    assert f'#{accepted} is open' in again.json()['message']


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
        ('PATCH', '', {'step': 'build'}),
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


def test_the_observe_scope_joins_a_session_to_a_worklog_another_client_opened(as_caller):
    """A reviewer's pod holds no worklog of its own, so its tokens reach the record only by joining the one its round names."""
    opened = as_caller.caller(PERSON).post('/worklogs', json={'record': as_caller.url('{issue}'), 'opened_by': 'claude-code/peer'})
    assert opened.status_code == status.HTTP_201_CREATED, show_status_and_response(opened)

    name = opened.json()['worklog']['name']
    joined = as_caller.caller(scoped(OBSERVE)).post(f'/worklogs/{name}/join', json={'session': RUN_POD_SESSION})
    assert joined.status_code == status.HTTP_200_OK, show_status_and_response(joined)
    assert RUN_POD_SESSION in [span['session'] for span in joined.json()['worklog']['spans']]


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


def test_the_documents_the_cli_reads_hold_what_each_scope_reaches():
    """The CLI's suite holds the scopes each command declares, and every request it sends, against these files.

    The first is the routes each scope reaches. The second is the bodies each scope may send to a
    route whose handler narrows it, which `refuse_a_scoped_update` reads. Its fake provider grants
    the scope the CLI spells, so a scope renamed here would otherwise pass both suites and answer
    403 to every scheduled run. A stale file is rewritten here and the test fails, so the change is
    committed beside the table. A scope reaching the issue update with no bodies of its own would be
    refused every one, so the two tables name the same scopes.
    """
    reaching = sorted(scope for scope, routes in SCOPE_ROUTES.items() if ISSUE_UPDATE in routes)
    assert reaching == sorted(SCOPED_UPDATES), f'{reaching} reach {ISSUE_UPDATE}, and SCOPED_UPDATES narrows {sorted(SCOPED_UPDATES)}'
    table = {scope: sorted(f'{method} {template}' for method, template in SCOPE_ROUTES[scope]) for scope in sorted(SCOPE_ROUTES)}
    bodies = {
        ' '.join(ISSUE_UPDATE): {
            scope: [
                {
                    'fields': sorted(body.fields),
                    **({'statuses': sorted(body.statuses)} if body.statuses else {}),
                    **({'from': sorted(body.from_statuses)} if body.from_statuses else {}),
                }
                for body in narrowing.bodies
            ]
            for scope, narrowing in SCOPED_UPDATES.items()
        },
    }
    stale = []
    for path, content in ((SCOPES_DOCUMENT, table), (SCOPED_BODIES_DOCUMENT, bodies)):
        document = json.dumps(content, indent=2) + '\n'
        if not path.exists() or path.read_text() != document:
            path.parent.mkdir(exist_ok=True)
            path.write_text(document)
            stale.append(path.name)
    assert not stale, f'{", ".join(stale)} did not hold what the API narrows to each scope and has been rewritten; commit it'


def test_the_documents_scope_adds_a_version_as_its_client_id_and_only_it_reaches_the_write(as_caller):
    url = as_caller.url('/documents/{issue}/plan')
    refused = as_caller.caller(scoped(COMMENTS)).put(url, json={'body': '2. Fix it better'})
    assert refused.status_code == status.HTTP_403_FORBIDDEN, show_status_and_response(refused)

    written = as_caller.caller(scoped(DOCUMENTS)).put(url, json={'body': '2. Fix it better'})
    assert written.status_code == status.HTTP_200_OK, show_status_and_response(written)
    version = written.json()['document']['version']
    assert (version['version'], version['written_by']) == (2, SERVICE)


@pytest.mark.parametrize(
    ('method', 'template'),
    [
        (method, f'/{noun}/pin')
        for method, noun in product(('POST', 'DELETE'), ('issues/{issue}', 'initiatives/{initiative}', 'ideas/{idea}'))
    ],
)
def test_no_scope_reaches_a_pin(as_caller, method, template):
    every_scope = ScopedClient(client_id=SERVICE, scopes=frozenset(SCOPE_ROUTES))
    response = as_caller.caller(every_scope).request(method, as_caller.url(template), json={})
    assert response.status_code == status.HTTP_403_FORBIDDEN, show_status_and_response(response)
