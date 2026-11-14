import datetime as dt

import pytest
from fastapi import status
from sqlalchemy import update

from slate import models
from tests.util import show_status_and_response


def ok(response, code=status.HTTP_200_OK):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()


def created(client, path, json) -> dict:
    return ok(client.post(path, json=json), status.HTTP_201_CREATED)


def a_pursuit(client, name) -> int:
    return created(client, '/pursuits', {'name': name, 'summary': f'{name} holds'})['pursuit']['number']


def an_initiative(client, name, pursuits) -> int:
    return created(client, '/initiatives', {'name': name, 'summary': f'{name} is done', 'pursuits': pursuits})['initiative']['number']


def output(key: str, written: dt.datetime, tokens: int) -> dict:
    return {'key': key, 'written_ts': written.isoformat(), 'model': 'claude-opus-5-5', 'tokens': {'output': tokens}}


def in_a_week() -> str:
    return (dt.datetime.now(dt.UTC).date() + dt.timedelta(days=7)).isoformat()


def test_a_focus_fills_the_shares_it_left_unnamed_and_reads_each_pursuits_factor_from_its_split_output(txn_api_logged_in):
    """An issue in an initiative in A draws 30, and an initiative in A and B draws 20, which each takes half of."""
    client, _ = txn_api_logged_in
    a, b, c = a_pursuit(client, 'Warehouse'), a_pursuit(client, 'Security'), a_pursuit(client, 'Home')
    in_a = an_initiative(client, 'Load the events', [a])
    in_a_and_b = an_initiative(client, 'Lock the loader', [a, b])
    issue = created(client, '/issues', {'title': 'Write the loader', 'initiative': in_a})['issue']['number']
    loose = created(client, '/issues', {'title': 'In no initiative'})['issue']['number']
    created(client, '/ideas', {'title': 'Light the hall', 'pursuits': [c]})

    focus = ok(
        client.put('/focus', json={'statement': 'Warehouse first', 'until_date': in_a_week(), 'shares': [{'pursuit': a, 'share': 0.5}]})
    )
    assert (focus['focus']['active'], focus['focus']['shares']) == (True, [{'pursuit': {'number': a, 'name': 'Warehouse'}, 'share': 0.5}])
    for session, record, tokens in (('s1', issue, 30), ('s2', in_a_and_b, 20), ('s3', loose, 7)):
        worklog = created(client, '/worklogs', {'record': record, 'opened_by': f'claude-code/{session}', 'session': session})['worklog']
        entered = dt.datetime.fromisoformat(worklog['spans'][0]['started_ts'])
        ok(client.put(f'/sessions/{session}/usage', json={'messages': [output(f'm_{session}', entered, tokens)]}))
    before_the_focus = output('m_before', entered - dt.timedelta(hours=1), 100)
    ok(client.put('/sessions/s4/usage', json={'messages': [output('m_outside', entered, 5), before_the_focus]}))

    standing = ok(client.get('/focus'))
    assert standing['window']['since'] == focus['focus']['started_ts']
    rows = [(row['number'], row['share'], row['share_named'], row['output_tokens'], row['worklog_count']) for row in standing['pursuits']]
    assert rows == [(c, 0.25, False, 0, 0), (b, 0.25, False, 10, 1), (a, 0.5, True, 40, 2)]
    assert [row['usage'] for row in standing['pursuits']] == pytest.approx([0, 0.2, 0.8])
    assert [row['factor'] for row in standing['pursuits']] == pytest.approx([1, 2**-0.8, 2**-1.6])
    assert [row['last_worked_ts'] is None for row in standing['pursuits']] == [True, False, False]
    assert standing['unattributed'] == {'output_tokens': 7, 'worklog_count': 1, 'outside_worklogs_output_tokens': 5}


def test_a_set_replaces_the_focus_in_force_and_one_past_its_until_date_reads_as_ended(txn_api_logged_in):
    client, session = txn_api_logged_in
    pursuit = a_pursuit(client, 'Warehouse')
    first = ok(client.put('/focus', json={'statement': 'Warehouse first', 'until_date': in_a_week()}))['focus']
    second = ok(client.put('/focus', json={'statement': 'Then security', 'until_date': in_a_week()}))
    assert [effect['code'] for effect in second['side_effects']] == ['focus_ended']

    yesterday = dt.datetime.now(dt.UTC).date() - dt.timedelta(days=1)
    session.execute(update(models.Focus).where(models.Focus.id == second['focus']['id']).values(until_date=yesterday))
    standing = ok(client.get('/focus'))
    assert standing['focus'] is None
    assert [(row['number'], row['share'], row['factor']) for row in standing['pursuits']] == [(pursuit, None, None)]

    ended = ok(client.post('/focus/end', json={'reason': 'Security landed'}))
    assert ([warning['code'] for warning in ended['warnings']], ended['focus']['ended_reason']) == (['focus_lapsed'], 'Security landed')
    nothing = ok(client.post('/focus/end', json={'reason': 'Again'}))
    assert (nothing['focus'], [warning['code'] for warning in nothing['warnings']]) == (None, ['no_focus_to_end'])

    history = ok(client.get('/focus/history'))
    assert [(focus['id'], focus['ended_reason'], focus['active']) for focus in history] == [
        (second['focus']['id'], 'Security landed', False),
        (first['id'], 'replaced', False),
    ]


def test_a_set_is_refused_a_pursuit_not_active_shares_past_one_and_an_until_date_already_past(txn_api_logged_in):
    client, _ = txn_api_logged_in
    active, other = a_pursuit(client, 'Warehouse'), a_pursuit(client, 'Security')
    shelved = created(client, '/pursuits', {'name': 'Home', 'summary': 'Home holds', 'status': 'someday'})['pursuit']['number']
    yesterday = (dt.datetime.now(dt.UTC).date() - dt.timedelta(days=1)).isoformat()
    for shares, until, refusal in (
        ([{'pursuit': shelved, 'share': 0.2}], in_a_week(), f'#{shelved} Home is someday'),
        ([{'pursuit': active, 'share': 0.6}, {'pursuit': other, 'share': 0.5}], in_a_week(), 'The shares sum to 1.1, past 1'),
        ([], yesterday, 'is before today'),
    ):
        response = client.put('/focus', json={'statement': 'Refused', 'until_date': until, 'shares': shares})
        assert response.status_code == status.HTTP_422_UNPROCESSABLE_CONTENT, show_status_and_response(response)
        assert refusal in response.json()['message']
    assert ok(client.get('/focus/history')) == []
