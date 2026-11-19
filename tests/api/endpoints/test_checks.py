"""Checks on an issue or an initiative, the mark asking for the next one, and the order records come due."""

import datetime as dt

from fastapi import status

from slate import models
from slate.config import get_settings
from tests.conftest import TEST_PERSON
from tests.session_database import get_test_runner_settings
from tests.util import show_status_and_response


def ok(response, code=status.HTTP_200_OK):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()


def issue(client, title: str, **fields) -> int:
    return ok(client.post('/issues', json={'title': title, **fields}), status.HTTP_201_CREATED)['issue']['number']


def checked_days_ago(session, number: int, days: int) -> None:
    session.add(
        models.RecordCheck(
            record_number=number,
            checked_ts=dt.datetime.now(dt.UTC) - dt.timedelta(days=days),
            checked_by='slate-svc-recheck',
            verdict='holds',
            evidence='It still describes work to do.',
        )
    )
    session.flush()


def test_each_record_comes_due_at_the_first_rung_it_meets_and_held_ideas_and_closed_never(txn_api_logged_in):
    client, session = txn_api_logged_in
    settings = get_test_runner_settings()
    settings.checks.head_rows = 2
    client.app.dependency_overrides[get_settings] = lambda: settings

    # The queue's head is the two urgent issues, so every priority-4 record sits past it.
    seeded = {
        'incoming low': issue(client, 'incoming low', status='incoming', priority=4),
        'incoming urgent': issue(client, 'incoming urgent', status='incoming', priority=1),
        'head checked 8 days ago': issue(client, 'head checked 8 days ago', priority=1),
        'head checked 2 days ago': issue(client, 'head checked 2 days ago', priority=1),
        'never checked, high': issue(client, 'never checked, high', priority=2),
        'never checked, low': issue(client, 'never checked, low', priority=4),
        'requested first': issue(client, 'requested first', priority=4),
        'requested second': issue(client, 'requested second', priority=4),
        'tail checked 31 days ago': issue(client, 'tail checked 31 days ago', priority=4),
        'tail checked 40 days ago': issue(client, 'tail checked 40 days ago', priority=4),
        'tail checked 8 days ago': issue(client, 'tail checked 8 days ago', priority=4),
        'claimed': issue(client, 'claimed', priority=4),
        'completed': issue(client, 'completed', priority=4),
    }
    initiative = ok(client.post('/initiatives', json={'name': 'never checked initiative', 'summary': 'It ships.', 'priority': 3}), 201)
    ok(client.post('/ideas', json={'title': 'an idea nobody took on'}), status.HTTP_201_CREATED)
    for title, days in (
        ('head checked 8 days ago', 8),
        ('head checked 2 days ago', 2),
        ('requested first', 1),
        ('tail checked 31 days ago', 31),
        ('tail checked 40 days ago', 40),
        ('tail checked 8 days ago', 8),
    ):
        checked_days_ago(session, seeded[title], days)
    for title in ('requested first', 'requested second'):
        ok(client.put(f'/records/{seeded[title]}/check-request', json={'reason': 'its paths moved'}))
    ok(client.post('/claims', json={'record': seeded['claimed'], 'holder': 'claude-code/peer'}))
    ok(client.patch(f'/issues/{seeded["completed"]}', json={'status': 'completed'}))

    due = ok(client.get('/checks/due'))

    assert [(row['title'], row['due_because']) for row in due] == [
        ('incoming urgent', 'incoming'),
        ('incoming low', 'incoming'),
        ('requested first', 'requested'),
        ('requested second', 'requested'),
        ('never checked, high', 'never_checked'),
        ('never checked initiative', 'never_checked'),
        ('never checked, low', 'never_checked'),
        ('head checked 8 days ago', 'queue_head'),
        ('tail checked 40 days ago', 'stale'),
        ('tail checked 31 days ago', 'stale'),
    ]
    assert due[5]['kind'] == 'initiative' and due[5]['number'] == initiative['initiative']['number']
    assert [row['title'] for row in ok(client.get('/checks/due?limit=3'))] == ['incoming urgent', 'incoming low', 'requested first']


def test_the_queue_and_every_read_carry_the_latest_check_and_a_check_clears_the_mark(txn_api_logged_in):
    client, _ = txn_api_logged_in
    number = issue(client, 'Retry the export on a timeout', priority=2)
    initiative = ok(client.post('/initiatives', json={'name': 'Ship the exporter', 'summary': 'It ships.'}), 201)['initiative']['number']

    marked = ok(client.put(f'/records/{number}/check-request', json={'reason': 'the exporter was rewritten'}))
    assert marked['check_request']['requested_by'] == TEST_PERSON.actor
    assert ok(client.get(f'/records/{number}'))['record']['check_request']['reason'] == 'the exporter was rewritten'

    first = {
        'verdict': 'holds',
        'evidence': 'The exporter still gives up on a timeout.',
        'cites': [{'kind': 'path', 'ref': 'slate:src/x.py'}],
    }
    written = ok(client.post(f'/records/{number}/checks', json=first), status.HTTP_201_CREATED)
    assert [effect['code'] for effect in written['side_effects']] == ['check_request_cleared']
    latest = {'verdict': 'corrected', 'evidence': 'The timeout moved to the client.', 'cites': [{'kind': 'issue', 'ref': '12'}]}
    ok(client.post(f'/records/{number}/checks', json=latest), status.HTTP_201_CREATED)
    ok(client.post(f'/records/{initiative}/checks', json={'verdict': 'contradicts', 'evidence': 'A standard rules it out.'}), 201)

    rows = {row['number']: row['record'] for row in ok(client.get('/queue'))['queue']}
    assert (rows[number]['last_check']['verdict'], rows[number]['last_check']['checked_by']) == ('corrected', TEST_PERSON.actor)
    assert rows[initiative]['last_check']['verdict'] == 'contradicts'
    shown = ok(client.get(f'/records/{number}'))['record']
    assert (shown['last_check']['verdict'], shown['check_request']) == ('corrected', None)
    assert [check['verdict'] for check in ok(client.get(f'/records/{number}/checks'))] == ['corrected', 'holds']
