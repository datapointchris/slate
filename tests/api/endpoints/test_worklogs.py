import datetime as dt

import pytest
from fastapi import status

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


def test_a_session_holding_one_issue_then_another_leaves_two_worklogs_each_with_its_span_and_the_first_its_commit(client):
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
    assert (on_bug['state'], on_bug['stage'], on_chore['stage']) == ('open', 'committed', 'started')


def test_a_worklog_started_with_no_claim_warns_of_another_open_one_and_ends_by_hand_or_with_its_record(client):
    idea = ok(client.post('/ideas', json={'title': 'Review rounds on a schedule'}), status.HTTP_201_CREATED)['idea']['number']

    started = ok(client.post(WORKLOGS, json={'record': idea, 'opened_by': 'claude-code/s2', 'session': 's2'}), status.HTTP_201_CREATED)
    discussion = started['worklog']
    assert (discussion['name'], discussion['claim'], discussion['state']) == (f'{idea}.1', None, 'open')
    second = ok(client.post(WORKLOGS, json={'record': idea, 'opened_by': 'chris@desk'}), status.HTTP_201_CREATED)
    assert codes(second['warnings']) == [('open_worklog', idea)]

    finished = ok(client.post(f'{WORKLOGS}/{idea}.2/finish', json={'finished_by': 'chris@desk'}))['worklog']
    assert (finished['outcome'], finished['ended_by'], finished['state']) == ('finished', 'chris@desk', 'ended')

    promoted = ok(client.post(f'/ideas/{idea}/promote', json={}))
    assert ('worklog_ended', idea) in codes(promoted['side_effects'])
    ended = ok(client.get(f'{WORKLOGS}/{idea}.1'))
    assert (ended['outcome'], [span['ended_ts'] for span in ended['spans']]) == ('promoted', [ended['ended_ts']])
    assert current(client, 's2') is None
