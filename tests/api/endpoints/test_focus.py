import datetime as dt
from zoneinfo import ZoneInfo

import pytest
from fastapi import status
from sqlalchemy import update

from slate import models
from slate.api.endpoints import focus as focus_routes
from tests.util import show_status_and_response

# The figures a day of `GET /focus/usage` shares with a pursuit's row in `GET /focus`.
STANDING_FIGURES = ('number', 'share', 'share_named', 'split_output_tokens', 'worklog_count', 'usage', 'factor')


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


class Clock:
    """The instant the focus routes read as now, which a test moves."""

    def __init__(self, monkeypatch, now: dt.datetime):
        self.now = now.astimezone(dt.UTC)
        monkeypatch.setattr(focus_routes, 'calendar_now', self.read)

    def read(self, zone: str) -> tuple[dt.date, dt.datetime]:
        return self.now.astimezone(ZoneInfo(zone)).date(), self.now


def wrote(client, session, record: int, outputs: dict[dt.datetime, int]) -> None:
    """One worklog on the record, holding a message of each output written at its instant."""
    name = f'on-{record}'
    worklog = created(client, '/worklogs', {'record': record, 'opened_by': f'claude-code/{name}', 'session': name})['worklog']
    entered = dt.datetime.fromisoformat(worklog['spans'][0]['started_ts'])
    messages = [output(f'{name}-{index}', entered, tokens) for index, tokens in enumerate(outputs.values())]
    ok(client.put(f'/sessions/{name}/usage', json={'messages': messages}))
    for index, written in enumerate(outputs):
        session.execute(update(models.MessageTokens).where(models.MessageTokens.key == f'{name}-{index}').values(written_ts=written))


def by_pursuit(day: dict, field: str) -> dict:
    return {row['number']: row[field] for row in day['pursuits']}


def standing_figures(rows: list[dict]) -> list[dict]:
    return [{field: row[field] for field in STANDING_FIGURES} for row in rows]


def utc(moment: str) -> dt.datetime:
    return dt.datetime.fromisoformat(moment).replace(tzinfo=dt.UTC)


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
    rows = [
        (row['number'], row['share'], row['share_named'], row['split_output_tokens'], row['worklog_count']) for row in standing['pursuits']
    ]
    assert rows == [(c, 0.25, False, 0, 0), (b, 0.25, False, 10, 1), (a, 0.5, True, 40, 2)]
    assert [row['usage'] for row in standing['pursuits']] == pytest.approx([0, 0.2, 0.8])
    assert [row['factor'] for row in standing['pursuits']] == pytest.approx([1, 2**-0.8, 2**-1.6])
    assert [row['touched_ts'] is None for row in standing['pursuits']] == [True, False, False]
    assert standing['unattributed'] == {'output_tokens': 7, 'worklog_count': 1, 'outside_worklogs_output_tokens': 5}


def test_a_set_replaces_the_focus_in_force_and_one_past_its_until_date_reads_as_ended(txn_api_logged_in):
    """A Job that starts twice sends its PUT twice, and the second answers the focus the first set."""
    client, session = txn_api_logged_in
    pursuit = a_pursuit(client, 'Warehouse')
    first = ok(client.put('/focus', json={'statement': 'Warehouse first', 'until_date': in_a_week()}))['focus']
    body = {'statement': 'Then security', 'until_date': in_a_week(), 'shares': [{'pursuit': pursuit, 'share': 0.5}]}
    second = ok(client.put('/focus', json=body))
    assert [effect['code'] for effect in second['side_effects']] == ['focus_replaced']
    retried = ok(client.put('/focus', json=body))
    assert (retried['focus']['id'], retried['side_effects'], [warning['code'] for warning in retried['warnings']]) == (
        second['focus']['id'],
        [],
        ['focus_unchanged'],
    )

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
    someday = created(client, '/pursuits', {'name': 'Home', 'summary': 'Home holds', 'status': 'someday'})['pursuit']['number']
    yesterday = (dt.datetime.now(dt.UTC).date() - dt.timedelta(days=1)).isoformat()
    for shares, until, refusal in (
        ([{'pursuit': someday, 'share': 0.2}], in_a_week(), f'#{someday} Home is someday'),
        ([{'pursuit': active, 'share': 0.6}, {'pursuit': other, 'share': 0.5}], in_a_week(), 'The shares sum to 1.1, past 1'),
        ([], yesterday, 'is before today'),
    ):
        response = client.put('/focus', json={'statement': 'Refused', 'until_date': until, 'shares': shares})
        assert response.status_code == status.HTTP_422_UNPROCESSABLE_CONTENT, show_status_and_response(response)
        assert refusal in response.json()['message']
    assert ok(client.get('/focus/history')) == []


def test_each_day_of_a_focus_reads_its_own_window_and_the_last_day_of_the_focus_in_force_is_the_standing(txn_api_logged_in, monkeypatch):
    """The first focus holds from 06-01 12:00 until the second replaces it at 06-03 12:00, read at 06-04 18:00 UTC."""
    client, session = txn_api_logged_in
    clock = Clock(monkeypatch, utc('2026-06-01T12:00'))
    a, b = a_pursuit(client, 'Warehouse'), a_pursuit(client, 'Security')
    in_a, in_b = an_initiative(client, 'Load the events', [a]), an_initiative(client, 'Lock the loader', [b])
    body = {'statement': 'Warehouse first', 'until_date': '2026-06-30', 'shares': [{'pursuit': a, 'share': 0.5}]}
    first = ok(client.put('/focus', json=body))['focus']
    clock.now = utc('2026-06-03T12:00')
    second = ok(client.put('/focus', json={'statement': 'Then security', 'until_date': '2026-06-30'}))['focus']
    clock.now = utc('2026-06-04T18:00')
    wrote(client, session, in_a, {utc('2026-06-01T10:00'): 100, utc('2026-06-01T14:00'): 30, utc('2026-06-03T15:00'): 50})
    wrote(client, session, in_b, {utc('2026-06-02T14:00'): 10, utc('2026-06-03T09:00'): 40, utc('2026-06-04T10:00'): 60})

    replaced = ok(client.get('/focus/usage', params={'focus': first['id']}))
    assert [replaced['since'], replaced['until']] == [first['started_ts'], second['started_ts']]
    assert {day['day']: (by_pursuit(day, 'split_output_tokens_on_day'), by_pursuit(day, 'usage')) for day in replaced['days']} == {
        '2026-06-01': ({a: 30, b: 0}, {a: 1.0, b: 0.0}),
        '2026-06-02': ({a: 0, b: 10}, {a: 0.75, b: 0.25}),
        '2026-06-03': ({a: 0, b: 40}, {a: 0.375, b: 0.625}),
    }
    last = replaced['days'][-1]
    assert [(row['number'], row['share'], row['share_named']) for row in last['pursuits']] == [(a, 0.5, True), (b, 0.5, False)]
    assert [row['factor'] for row in last['pursuits']] == pytest.approx([2**-0.75, 2**-1.25])

    in_force = ok(client.get('/focus/usage'))
    assert (in_force['focus']['id'], [day['day'] for day in in_force['days']]) == (second['id'], ['2026-06-03', '2026-06-04'])
    assert by_pursuit(in_force['days'][0], 'split_output_tokens') == {a: 50, b: 0}
    standing = ok(client.get('/focus'))
    assert in_force['days'][-1]['window'] == standing['window']
    assert standing_figures(in_force['days'][-1]['pursuits']) == standing_figures(standing['pursuits'])

    missing = client.get('/focus/usage', params={'focus': second['id'] + 1})
    assert missing.status_code == status.HTTP_404_NOT_FOUND, show_status_and_response(missing)


def test_a_focus_older_than_thirty_days_answers_its_last_thirty_and_each_window_holds_720_hours_across_a_change_of_clocks(
    txn_api_logged_in, monkeypatch
):
    """America/New_York leaves daylight time on 2026-11-01, so 720 hours before 11-06's midnight is 01:00 on 10-07."""
    client, session = txn_api_logged_in
    new_york = ZoneInfo('America/New_York')
    zone = {'timezone': 'America/New_York'}
    clock = Clock(monkeypatch, dt.datetime(2026, 9, 1, 9, tzinfo=new_york))
    a, b = a_pursuit(client, 'Warehouse'), a_pursuit(client, 'Security')
    in_a, in_b = an_initiative(client, 'Load the events', [a]), an_initiative(client, 'Lock the loader', [b])
    body = {'statement': 'Warehouse first', 'until_date': '2026-12-31', 'shares': [{'pursuit': a, 'share': 0.5}]}
    ok(client.put('/focus', params=zone, json=body))
    clock.now = dt.datetime(2026, 11, 20, 15, 30, tzinfo=new_york).astimezone(dt.UTC)
    opens = clock.now - dt.timedelta(hours=720)
    wrote(client, session, in_a, {opens - dt.timedelta(minutes=1): 7, opens + dt.timedelta(minutes=1): 11})
    before_one, after_one = dt.datetime(2026, 10, 7, 0, 30, tzinfo=new_york), dt.datetime(2026, 10, 7, 1, 30, tzinfo=new_york)
    wrote(client, session, in_b, {before_one: 13, after_one: 17, clock.now - dt.timedelta(hours=1): 5})

    usage = ok(client.get('/focus/usage', params=zone))
    assert (len(usage['days']), usage['days'][0]['day'], usage['days'][-1]['day']) == (30, '2026-10-22', '2026-11-20')
    after_the_change = {day['day']: day for day in usage['days']}['2026-11-05']
    since, until = (dt.datetime.fromisoformat(after_the_change['window'][bound]) for bound in ('since', 'until'))
    assert (since, until - since) == (dt.datetime(2026, 10, 7, 1, tzinfo=new_york), dt.timedelta(hours=720))
    assert by_pursuit(after_the_change, 'split_output_tokens') == {a: 18, b: 17}
    standing = ok(client.get('/focus', params=zone))
    assert by_pursuit(usage['days'][-1], 'split_output_tokens') == {a: 11, b: 5}
    assert standing_figures(usage['days'][-1]['pursuits']) == standing_figures(standing['pursuits'])

    ok(client.post('/focus/end', params=zone, json={'reason': 'Warehouse landed'}))
    unfocused = ok(client.get('/focus/usage', params=zone))
    assert (unfocused['focus'], len(unfocused['days']), {row['share'] for row in unfocused['days'][-1]['pursuits']}) == (None, 30, {None})
    assert standing_figures(unfocused['days'][-1]['pursuits']) == standing_figures(ok(client.get('/focus', params=zone))['pursuits'])
