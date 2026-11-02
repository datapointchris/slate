import datetime as dt

import pytest
from fastapi import status

from tests.session_database import insert_test_data_transactional
from tests.util import show_status_and_response


@pytest.fixture
def client(txn_api_logged_in):
    client, session = txn_api_logged_in
    insert_test_data_transactional(session, 'issues')
    return client


def ok(response, code=status.HTTP_200_OK):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()


def number(client, title: str) -> int:
    return next(row['number'] for row in ok(client.get('/issues', params={'status': 'all'})) if row['title'] == title)


def message(key: str, written: dt.datetime, uncached_input: int, cache_write: int, cache_read: int, output: int) -> dict:
    tokens = {'uncached_input': uncached_input, 'cache_write': cache_write, 'cache_read': cache_read, 'output': output}
    return {'key': key, 'written_ts': written.isoformat(), 'model': 'claude-opus-5-5', 'tokens': tokens}


def test_a_session_in_two_worklogs_reports_once_each_worklog_sums_its_span_and_reporting_again_changes_nothing(client):
    """A message goes to the span holding its moment, a moment outside every span to the session alone."""
    bug = number(client, 'Ready bug with high priority')
    chore = number(client, 'Ready chore with low priority')
    start = {'opened_by': 'claude-code/s1', 'session': 's1'}
    first = ok(client.post('/worklogs', json={'record': bug, **start}), status.HTTP_201_CREATED)['worklog']
    second = ok(client.post('/worklogs', json={'record': chore, **start}), status.HTTP_201_CREATED)['worklog']
    entered_first = dt.datetime.fromisoformat(first['spans'][0]['started_ts'])
    entered_second = dt.datetime.fromisoformat(second['spans'][0]['started_ts'])

    report = {
        'host': 'desk',
        'messages': [
            message('msg_before', entered_first - dt.timedelta(minutes=5), 3, 30_000, 0, 120),
            message('msg_first', entered_first, 2, 400, 31_000, 90),
            message('msg_second', entered_second + dt.timedelta(minutes=1), 1, 600, 32_000, 75),
            {**message('msg_subagent', entered_second + dt.timedelta(minutes=2), 4, 9_000, 1_000, 300), 'subagent': 'a1'},
        ],
    }
    reported = ok(client.put('/sessions/s1/usage', json=report))
    assert reported['warnings'] == []

    tokens = {name: ok(client.get(f'/worklogs/{name}'))['tokens'] for name in (f'{bug}.1', f'{chore}.1')}
    assert tokens == {
        f'{bug}.1': {'uncached_input': 2, 'cache_write': 400, 'cache_read': 31_000, 'output': 90},
        f'{chore}.1': {'uncached_input': 5, 'cache_write': 9_600, 'cache_read': 33_000, 'output': 375},
    }
    held = reported['session']
    assert held['messages'] == 4
    assert held['tokens_outside_worklogs'] == {'uncached_input': 3, 'cache_write': 30_000, 'cache_read': 0, 'output': 120}
    assert held['tokens'] == {'uncached_input': 10, 'cache_write': 40_000, 'cache_read': 64_000, 'output': 585}

    again = ok(client.put('/sessions/s1/usage', json=report))
    assert [warning['code'] for warning in again['warnings']] == ['usage_unchanged']
    assert again['session'] == held
