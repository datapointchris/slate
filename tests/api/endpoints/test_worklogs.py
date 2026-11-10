import datetime as dt

import pytest
from fastapi import status
from sqlalchemy import update

from slate import models
from tests.session_database import insert_test_data_transactional
from tests.util import show_status_and_response

WORKLOGS = '/worklogs'


@pytest.fixture
def client(txn_api_logged_in):
    client, session = txn_api_logged_in
    insert_test_data_transactional(session, 'issues', 'issue_labels', 'issue_initiatives')
    return client


def ok(response, code=status.HTTP_200_OK):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()


def number(client, title: str) -> int:
    return next(row['number'] for row in ok(client.get('/issues', params={'status': 'all'})) if row['title'] == title)


def codes(effects: list[dict]) -> list[tuple[str, int | None]]:
    return [(effect['code'], effect['record']) for effect in effects]


def current(client, session: str) -> str | None:
    worklog = ok(client.get(f'{WORKLOGS}/current', params={'session': session}))
    return worklog and worklog['name']


def test_a_session_holding_one_issue_then_another_leaves_two_worklogs_and_resuming_the_first_takes_it_back(client):
    """Each worklog lists the session with its span, and the first its commit. Resuming the parked first returns the session to it."""
    bug = number(client, 'Ready bug with high priority')
    chore = number(client, 'Ready chore with low priority')
    where = {'holder': 'claude-code/s1', 'session': 's1', 'host': 'desk'}

    first = ok(client.post('/claims', json={'record': bug, 'directory': '/work/bug', **where}))
    assert (codes(first['side_effects']), first['claim']['worklog']) == ([('worklog_opened', bug)], f'{bug}.1')
    assert current(client, 's1') == f'{bug}.1'
    commit = {'kind': 'commit', 'ref': '4fca8f1', 'repo': 'slate', 'session': 's1', 'linked_by': 'claude-code/s1'}
    ok(client.post(f'{WORKLOGS}/{bug}.1/links', json=commit))

    second = ok(client.post('/claims', json={'record': chore, 'directory': '/work/chore', **where}))
    assert codes(second['side_effects']) == [('worklog_opened', chore), ('session_left_worklog', bug)]
    assert current(client, 's1') == f'{chore}.1'

    on_bug = ok(client.get(f'{WORKLOGS}/{bug}.1'))
    on_chore = ok(client.get(f'{WORKLOGS}/{chore}.1'))
    (left,) = on_bug['spans']
    (joined,) = on_chore['spans']
    assert (left['session'], left['directory'], joined['session'], joined['directory']) == ('s1', '/work/bug', 's1', '/work/chore')
    assert joined['ended_ts'] is None
    assert dt.datetime.fromisoformat(left['ended_ts']) == dt.datetime.fromisoformat(joined['started_ts'])
    assert [(link['kind'], link['ref'], link['session']) for link in on_bug['links']] == [('commit', '4fca8f1', 's1')]
    assert (on_bug['state'], on_bug['stage'], on_chore['stage']) == ('live', 'committed', 'started')

    ok(client.post(f'/claims/{bug}/wait', json={'holder': 'claude-code/s1', 'waiting_on': 'slate#3 to merge'}))
    resumed = ok(client.post(f'/claims/{bug}/resume', json={'holder': 'claude-code/s1', 'session': 's1'}))
    assert codes(resumed['side_effects']) == [('session_left_worklog', chore), ('session_rejoined_worklog', bug)]
    assert current(client, 's1') == f'{bug}.1'


def test_a_worklog_started_with_no_claim_warns_of_another_open_one_and_ends_by_hand_or_with_its_record(client):
    idea = ok(client.post('/ideas', json={'title': 'Review rounds on a schedule'}), status.HTTP_201_CREATED)['idea']['number']

    started = ok(client.post(WORKLOGS, json={'record': idea, 'opened_by': 'claude-code/s2', 'session': 's2'}), status.HTTP_201_CREATED)
    discussion = started['worklog']
    assert (discussion['name'], discussion['claim'], discussion['state']) == (f'{idea}.1', None, 'live')
    second = ok(client.post(WORKLOGS, json={'record': idea, 'opened_by': 'chris@desk'}), status.HTTP_201_CREATED)
    assert codes(second['warnings']) == [('open_worklog', idea)]

    finished = ok(client.post(f'{WORKLOGS}/{idea}.2/finish', json={'finished_by': 'chris@desk'}))['worklog']
    assert (finished['outcome'], finished['ended_by'], finished['state']) == ('finished', 'chris@desk', 'ended')

    promoted = ok(client.post(f'/ideas/{idea}/promote', json={'summary': 'It is done.'}))
    assert ('worklog_ended', idea) in codes(promoted['side_effects'])
    ended = ok(client.get(f'{WORKLOGS}/{idea}.1'))
    assert (ended['outcome'], [span['ended_ts'] for span in ended['spans']]) == ('promoted', [ended['ended_ts']])
    assert current(client, 's2') is None


def test_a_step_is_checked_once_a_worklog_of_it_ends_unexpired_and_the_furthest_is_the_initiative_stage(txn_api_logged_in):
    """A hold names its step, so the initiative reads which steps it went through without a session remembering to record them."""
    client, session = txn_api_logged_in
    created = ok(client.post('/initiatives', json={'name': 'Record the steps', 'summary': 'Each step shows.'}), status.HTTP_201_CREATED)
    initiative = created['initiative']['number']
    holder = {'holder': 'claude-code/s3', 'session': 's3'}

    def read() -> tuple[str, list[tuple[str, str, str | None]]]:
        shown = ok(client.get(f'/initiatives/{initiative}'))
        return shown['stage'], [(step['step'], step['worklog'], step['session']) for step in shown['steps']]

    held = ok(client.post('/claims', json={'record': initiative, 'step': 'design', **holder}))
    assert held['side_effects'][0]['message'] == f'Opened worklog {initiative}.1 for step design, with session s3 in it.'
    assert (ok(client.get(f'{WORKLOGS}/{initiative}.1'))['step'], read()) == ('design', ('idea', []))
    again = ok(client.post('/claims', json={'record': initiative, 'step': 'preflight', **holder}))
    assert f'Its worklog {initiative}.1 keeps step design.' in again['warnings'][0]['message']
    ok(client.post(f'/claims/{initiative}/release', json={'released_by': 'claude-code/s3'}))
    assert read() == ('design', [('design', f'{initiative}.1', 's3')])

    ok(client.post(WORKLOGS, json={'record': initiative, 'opened_by': 'chris', 'step': 'shape'}), status.HTTP_201_CREATED)
    ok(client.post(f'{WORKLOGS}/{initiative}.2/finish', json={'finished_by': 'chris'}))
    assert read() == ('design', [('shape', f'{initiative}.2', None), ('design', f'{initiative}.1', 's3')])

    lapsing = ok(client.post('/claims', json={'record': initiative, 'step': 'preflight', **holder}))['claim']
    lapsed = dt.datetime.now(dt.UTC) - dt.timedelta(minutes=1)
    session.execute(
        update(models.Claim).where(models.Claim.id == lapsing['id']).values(started_ts=lapsed - dt.timedelta(hours=1), expires_ts=lapsed)
    )
    session.execute(
        update(models.Worklog).where(models.Worklog.claim_id == lapsing['id']).values(started_ts=lapsed - dt.timedelta(hours=1))
    )
    session.execute(
        update(models.WorklogSpan).where(models.WorklogSpan.session_id == 's3').values(started_ts=lapsed - dt.timedelta(hours=1))
    )
    session.flush()
    ok(client.post('/claims', json={'record': initiative, 'holder': 'claude-code/s4'}))
    listed = next(row for row in ok(client.get('/initiatives')) if row['number'] == initiative)
    boarded = next(row for row in ok(client.get('/initiatives/board'))['initiatives'] if row['number'] == initiative)
    assert (listed['stage'], [step['step'] for step in boarded['steps']]) == ('design', ['shape', 'design'])
