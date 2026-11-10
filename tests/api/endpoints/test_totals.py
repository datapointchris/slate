import datetime as dt

import pytest
from fastapi import status

from tests.util import show_status_and_response


@pytest.fixture
def client(txn_api_logged_in):
    client, _ = txn_api_logged_in
    return client


def ok(response, code=status.HTTP_200_OK):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()


def message(key: str, written: dt.datetime, uncached_input: int, cache_write: int, cache_read: int, output: int) -> dict:
    tokens = {'uncached_input': uncached_input, 'cache_write': cache_write, 'cache_read': cache_read, 'output': output}
    return {'key': key, 'written_ts': written.isoformat(), 'model': 'claude-opus-5-5', 'tokens': tokens}


def test_an_initiative_totals_its_issues_and_their_children_and_a_seven_day_window_counts_time_outside_every_worklog(client):
    """The window ends a minute into the span s1 holds, so the span's tokens split at the edge."""
    initiative = ok(client.post('/initiatives', json={'summary': 'It is done.', 'name': 'Totals'}), status.HTTP_201_CREATED)['initiative'][
        'number'
    ]
    issue = ok(client.post('/issues', json={'title': 'In it', 'initiative': initiative}), status.HTTP_201_CREATED)['issue']['number']
    child = ok(client.post('/issues', json={'title': 'Under it', 'parent': issue}), status.HTTP_201_CREATED)['issue']['number']

    ok(client.post('/claims', json={'record': issue, 'holder': 'claude-code/s1', 'session': 's1'}))
    ok(client.post('/worklogs', json={'record': child, 'opened_by': 'claude-code/s2', 'session': 's2'}), status.HTTP_201_CREATED)
    for kind, ref in (('commit', '4fca8f1'), ('commit', '9b2e0d7'), ('pull_request', 'https://example.com/pr/5'), ('landing', '9b2e0d7')):
        link = {'kind': kind, 'ref': ref, 'repo': 'slate', 'session': 's1', 'linked_by': 'claude-code/s1'}
        ok(client.post(f'/worklogs/{issue}.1/links', json=link))
    entered = dt.datetime.fromisoformat(ok(client.get(f'/worklogs/{issue}.1'))['spans'][0]['started_ts'])
    until = entered + dt.timedelta(minutes=1)

    ok(
        client.put(
            '/sessions/s1/usage',
            json={'messages': [message('m_inside', entered, 1, 10, 100, 5), message('m_past_edge', until, 2, 20, 200, 6)]},
        )
    )
    ok(client.put('/sessions/s2/usage', json={'messages': [message('m_child', entered + dt.timedelta(seconds=30), 4, 40, 400, 8)]}))
    outside = [
        message('m_outside', entered - dt.timedelta(hours=1), 8, 80, 800, 9),
        message('m_before_the_window', entered - dt.timedelta(days=8), 16, 0, 0, 1),
    ]
    ok(client.put('/sessions/s3/usage', json={'messages': outside}))

    whole = ok(client.get('/totals', params={'record': initiative}))
    assert whole['scope']['record']['number'] == initiative
    assert (whole['worklog_count'], whole['session_count'], whole['message_count']) == (2, 2, 3)
    assert whole['link_counts'] == {'commit': 2, 'pull_request': 1, 'landing': 1}
    assert whole['tokens_outside_worklogs'] is None
    assert whole['tokens'] == {'uncached_input': 7, 'cache_write': 70, 'cache_read': 700, 'output': 19}

    week = ok(client.get('/totals', params={'since': (until - dt.timedelta(days=7)).isoformat(), 'until': until.isoformat()}))
    assert (week['worklog_count'], week['session_count'], week['message_count']) == (2, 3, 3)
    assert week['link_counts'] == {'commit': 2, 'pull_request': 1, 'landing': 1}
    assert week['tokens'] == {'uncached_input': 13, 'cache_write': 130, 'cache_read': 1_300, 'output': 22}
    assert week['tokens_outside_worklogs'] == {'uncached_input': 8, 'cache_write': 80, 'cache_read': 800, 'output': 9}
    assert [(row['model'], row['message_count']) for row in week['tokens_by_model']] == [('claude-opus-5-5', 3)]


def test_a_record_totals_its_latest_preflight_estimate_where_a_kind_never_estimated_reads_null(client):
    """An issue's preflight estimates the issue, so the initiative reaching it does not read it."""
    initiative = ok(client.post('/initiatives', json={'summary': 'It is done.', 'name': 'Estimated'}), status.HTTP_201_CREATED)[
        'initiative'
    ]['number']
    issue = ok(client.post('/issues', json={'title': 'In it', 'initiative': initiative}), status.HTTP_201_CREATED)['issue']['number']
    for record in (initiative, initiative, issue):
        ok(client.post('/worklogs', json={'record': record, 'opened_by': 'chris', 'step': 'preflight'}), status.HTTP_201_CREATED)

    ok(client.patch(f'/worklogs/{initiative}.1', json={'estimate': {'output': 900000, 'cache_read': 5}}))
    edited = ok(client.patch(f'/worklogs/{initiative}.1', json={'estimate': {'cache_read': None, 'uncached_input': 0}}))
    estimate = {'uncached_input': 0, 'cache_write': None, 'cache_read': None, 'output': 900000}
    assert (edited['worklog']['estimate'], edited['warnings']) == (estimate, [])
    ok(client.patch(f'/worklogs/{issue}.1', json={'estimate': {'output': 1}}))
    off_preflight = ok(client.patch(f'/worklogs/{initiative}.2', json={'step': 'build', 'estimate': {'output': 7}}))
    assert [warning['code'] for warning in off_preflight['warnings']] == ['estimate_off_preflight']

    assert ok(client.get('/totals', params={'record': initiative}))['estimate'] == {'worklog': f'{initiative}.1', 'tokens': estimate}
    assert ok(client.get('/totals', params={'record': issue}))['estimate']['tokens']['output'] == 1
    cleared = ok(client.patch(f'/worklogs/{initiative}.1', json={'estimate': None}))
    assert (cleared['worklog']['estimate'], ok(client.get('/totals', params={'record': initiative}))['estimate']) == (None, None)
