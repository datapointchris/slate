import datetime as dt
from zoneinfo import ZoneInfo

import pytest
from fastapi import status
from sqlalchemy import event
from sqlalchemy import update

from slate import models
from slate import schemas
from slate.services.initiatives import ROUTED_NAMES
from tests.session_database import insert_test_data_transactional
from tests.util import show_status_and_response

from .crud_test import ApiCrudTester

INITIATIVES = '/initiatives'

NEW_INITIATIVE = schemas.InitiativeCreate(name='Move dev items into issues', summary='Every dev item is an issue.', priority=1)


@pytest.fixture
def client(txn_api_logged_in):
    client, session = txn_api_logged_in
    insert_test_data_transactional(session, 'issue_initiatives')
    return client


def ok(response, code=status.HTTP_200_OK):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()


def wrote(response, written, code=status.HTTP_200_OK):
    return ok(response, code)[written]


def names(rows) -> list[str]:
    return [row['name'] for row in rows]


def test_lifecycle(client):
    ApiCrudTester(endpoint=INITIATIVES, new_obj=NEW_INITIATIVE, written='initiative', expected_length=2).test_lifecycle(client)


def test_active_initiatives_order_by_priority_with_none_last(client):
    ok(client.post(INITIATIVES, json={'summary': 'It is done.', 'name': 'Urgent cleanup', 'priority': 1}), status.HTTP_201_CREATED)
    assert names(ok(client.get(INITIATIVES))) == ['Urgent cleanup', 'Ship the issue tracker', 'Retire the old capture path']


@pytest.mark.parametrize(
    ('sort', 'expected'),
    [
        ('open', ['Retire the old capture path', 'Ship the issue tracker', 'Started last']),
        ('newest', ['Started last', 'Retire the old capture path', 'Ship the issue tracker']),
    ],
)
def test_a_sort_reorders_the_active_initiatives(client, sort, expected):
    for title, initiative in (('a', 'Retire the old capture path'), ('b', 'Retire the old capture path'), ('c', 'Ship the issue tracker')):
        ok(client.post('/issues', json={'title': title, 'initiative': initiative}), status.HTTP_201_CREATED)
    ok(client.post(INITIATIVES, json={'summary': 'It is done.', 'name': 'Started last'}), status.HTTP_201_CREATED)
    assert names(ok(client.get(INITIATIVES, params={'sort': sort}))) == expected


def test_someday_initiatives_sit_between_the_active_and_the_finished(client):
    ok(
        client.post(INITIATIVES, json={'summary': 'It is done.', 'name': 'Urgent idea', 'status': 'someday', 'priority': 1}),
        status.HTTP_201_CREATED,
    )
    assert names(ok(client.get(INITIATIVES, params={'status': 'all'})))[-2:] == ['Urgent idea', 'Finished migration']


def test_an_initiative_answers_to_its_name(client):
    assert ok(client.get(f'{INITIATIVES}/Ship the issue tracker'))['priority'] == 2


def test_an_initiative_answers_to_its_number(client):
    number = ok(client.get(f'{INITIATIVES}/Ship the issue tracker'))['number']
    assert ok(client.get(f'{INITIATIVES}/{number}'))['name'] == 'Ship the issue tracker'
    filed = wrote(client.post('/issues', json={'title': 'filed by number', 'initiative': f'#{number}'}), 'issue', status.HTTP_201_CREATED)
    assert filed['initiative']['number'] == number


# The path is decoded before routing, so an escaped `%2F` arrives as a slash,
# and a route taking one segment answers 404.
def test_a_name_holding_a_slash_addresses_its_initiative(client):
    ok(client.post(INITIATIVES, json={'summary': 'It is done.', 'name': 'library/consumer pair'}), status.HTTP_201_CREATED)
    path = f'{INITIATIVES}/library%2Fconsumer%20pair'
    assert ok(client.get(path))['name'] == 'library/consumer pair'
    assert wrote(client.patch(path, json={'priority': 3}), 'initiative')['priority'] == 3
    assert client.delete(path).status_code == status.HTTP_200_OK
    assert client.get(path).status_code == status.HTTP_404_NOT_FOUND


def test_a_short_name_is_answered_with_the_initiative_it_meant(client):
    response = client.get(f'{INITIATIVES}/issue tracker')
    assert response.status_code == status.HTTP_404_NOT_FOUND, show_status_and_response(response)
    assert "'Ship the issue tracker'" in response.json()['message']


def test_counts_partition_the_issues_and_repos_name_the_work_done_or_left(client):
    created = {
        repo: wrote(
            client.post('/issues', json={'title': repo, 'repo': repo, 'initiative': 'Ship the issue tracker'}),
            'issue',
            status.HTTP_201_CREATED,
        )
        for repo in ('webapp', 'dotfiles', 'notes-cli')
    }
    ok(client.patch(f'/issues/{created["dotfiles"]["number"]}', json={'status': 'completed'}))
    ok(client.patch(f'/issues/{created["notes-cli"]["number"]}', json={'status': 'canceled', 'status_reason': 'Moved'}))

    counted = ok(client.get(f'{INITIATIVES}/Ship the issue tracker'))
    assert (counted['issue_count'], counted['open_count'], counted['completed_count'], counted['canceled_count']) == (3, 1, 1, 1)
    assert counted['repos'] == ['dotfiles', 'webapp']


def test_dropping_needs_a_reason(client):
    response = client.patch(f'{INITIATIVES}/Retire the old capture path', json={'status': 'dropped'})
    assert response.status_code == status.HTTP_422_UNPROCESSABLE_CONTENT, show_status_and_response(response)


def test_closing_stamps_and_reopening_clears(client):
    closed = wrote(
        client.patch(f'{INITIATIVES}/Retire the old capture path', json={'status': 'dropped', 'status_reason': 'Folded in'}), 'initiative'
    )
    assert closed['closed_ts'] is not None
    reopened = wrote(client.patch(f'{INITIATIVES}/{closed["id"]}', json={'status': 'active'}), 'initiative')
    assert (reopened['closed_ts'], reopened['status_reason']) == (None, None)


def test_only_one_active_initiative_holds_a_name(client):
    response = client.post(INITIATIVES, json={'summary': 'It is done.', 'name': 'Ship the issue tracker'})
    assert response.status_code == status.HTTP_409_CONFLICT, show_status_and_response(response)
    ok(client.post(INITIATIVES, json={'summary': 'It is done.', 'name': 'Finished migration'}), status.HTTP_201_CREATED)


def test_a_closed_initiative_stops_lending_its_priority(client):
    created = wrote(
        client.post('/issues', json={'title': 'inherits', 'initiative': 'Ship the issue tracker'}), 'issue', status.HTTP_201_CREATED
    )
    assert created['effective_priority'] == 2
    ok(client.patch(f'{INITIATIVES}/Ship the issue tracker', json={'status': 'completed'}))
    assert ok(client.get(f'/issues/{created["number"]}'))['effective_priority'] == 0


def test_deleting_an_initiative_keeps_its_issues(client):
    created = wrote(
        client.post('/issues', json={'title': 'kept', 'initiative': 'Ship the issue tracker'}), 'issue', status.HTTP_201_CREATED
    )
    answer = ok(client.delete(f'{INITIATIVES}/Ship the issue tracker'))
    assert [(effect['code'], effect['record']) for effect in answer['side_effects']] == [('issue_detached', created['number'])]
    assert ok(client.get(f'/issues/{created["number"]}'))['initiative'] is None


def numbers(rows) -> list[int]:
    return [row['number'] for row in rows]


def test_progress_counts_each_issue_in_one_state_and_lists_what_runs_waits_is_blocked_comes_next_and_closed(client):
    """The running issue and the triage issue each depend on an open one, and neither counts as blocked."""
    initiative = wrote(
        client.post(INITIATIVES, json={'summary': 'It is done.', 'name': 'Progress', 'priority': 2}), 'initiative', status.HTTP_201_CREATED
    )['number']

    def filed(title: str, **fields) -> int:
        issue = {'title': title, 'initiative': initiative, **fields}
        return wrote(client.post('/issues', json=issue), 'issue', status.HTTP_201_CREATED)['number']

    ready = filed('Ready')
    completed, canceled, waiting = filed('Completed'), filed('Canceled'), filed('Waiting')
    running = filed('Running', depends_on=[ready])
    triage = filed('Triage', status='triage', depends_on=[ready])
    blocked = filed('Blocked', depends_on=[ready])
    parent = filed('Parent')
    filed('Deferred', parent=parent, deferred_until_date='2099-01-01')
    ok(client.patch(f'/issues/{completed}', json={'status': 'completed'}))
    ok(client.patch(f'/issues/{canceled}', json={'status': 'canceled', 'status_reason': 'not needed'}))
    ok(client.post('/claims', json={'record': running, 'holder': 'claude-code/s1', 'session': 's1'}))
    ok(client.post('/claims', json={'record': waiting, 'holder': 'claude-code/s2'}))
    ok(client.post(f'/claims/{waiting}/wait', json={'holder': 'claude-code/s2', 'waiting_on': 'a review'}))
    entered = dt.datetime.fromisoformat(ok(client.get(f'/worklogs/{running}.1'))['spans'][0]['started_ts'])
    tokens = {'uncached_input': 1, 'cache_write': 10, 'cache_read': 100, 'output': 5}
    message = {'key': 'm1', 'written_ts': (entered + dt.timedelta(seconds=1)).isoformat(), 'model': 'claude-opus-5-5', 'tokens': tokens}
    ok(client.put('/sessions/s1/usage', json={'messages': [message]}))

    (progress,) = ok(client.get(f'{INITIATIVES}/progress', params={'initiative': 'Progress'}))
    assert progress['initiative']['number'] == initiative
    assert progress['states'] == {
        'completed': 1,
        'canceled': 1,
        'running': 1,
        'waiting': 1,
        'ready': 1,
        'blocked': 1,
        'triage': 1,
        'deferred': 1,
        'shelved': 0,
        'open_children': 1,
    }
    assert sum(progress['states'].values()) == progress['initiative']['issue_count']
    assert (numbers(progress['running']), numbers(progress['waiting']), numbers(progress['blocked'])) == ([running], [waiting], [blocked])
    assert progress['waiting'][0]['claim']['waiting']['waiting_on'] == 'a review'
    assert numbers(progress['next']) == [ready]
    assert numbers(progress['closed']) == [canceled, completed]
    assert triage not in numbers(progress['blocked'])
    totals = progress['totals']
    assert (totals['scope']['record']['number'], totals['worklog_count'], totals['tokens']) == (initiative, 2, tokens)

    later = (dt.datetime.now(dt.UTC) + dt.timedelta(minutes=1)).isoformat()
    (windowed,) = ok(client.get(f'{INITIATIVES}/progress', params={'initiative': initiative, 'since': later}))
    assert (windowed['closed'], windowed['closed_since'] is not None, windowed['states']) == ([], True, progress['states'])

    every_active = ok(client.get(f'{INITIATIVES}/progress'))
    assert [row['initiative']['number'] for row in every_active] == [row['number'] for row in ok(client.get(INITIATIVES))]


def test_the_burnup_counts_an_issue_moved_in_from_its_move_and_a_canceled_one_out_from_its_close(client, txn_api_logged_in):
    _, session = txn_api_logged_in

    def created(name: str) -> int:
        return wrote(client.post(INITIATIVES, json={'summary': 'It is done.', 'name': name}), 'initiative', status.HTTP_201_CREATED)[
            'number'
        ]

    def filed(initiative: int, title: str) -> int:
        return wrote(client.post('/issues', json={'title': title, 'initiative': initiative}), 'issue', status.HTTP_201_CREATED)['number']

    burning, elsewhere = created('Burning'), created('Elsewhere')
    kept, done, dropped, moved = filed(burning, 'Kept'), filed(burning, 'Done'), filed(burning, 'Dropped'), filed(elsewhere, 'Moved in')
    ok(client.patch(f'/issues/{moved}', json={'initiative': burning}))
    ok(client.patch(f'/issues/{done}', json={'status': 'completed'}))
    ok(client.patch(f'/issues/{dropped}', json={'status': 'canceled', 'status_reason': 'not needed'}))
    now = dt.datetime.now(dt.UTC)
    filed_at, moved_at, done_at, dropped_at = (now - dt.timedelta(days=days) for days in (10, 5, 3, 2))
    change = models.RecordChange
    session.execute(update(models.Issue).where(models.Issue.number.in_([kept, done, dropped, moved])).values(created_ts=filed_at))
    session.execute(update(models.Issue).where(models.Issue.number == done).values(closed_ts=done_at))
    session.execute(update(models.Issue).where(models.Issue.number == dropped).values(closed_ts=dropped_at))
    session.execute(update(change).where(change.table_name == 'issues').values(changed_ts=filed_at))
    moves = update(change).where(change.table_name == 'issues', change.operation == 'update', change.changes.has_key('initiative_id'))
    session.execute(moves.values(changed_ts=moved_at))
    session.flush()

    (progress,) = ok(client.get(f'{INITIATIVES}/progress', params={'initiative': burning}))
    steps = progress['burnup']
    assert [(step['scope'], step['completed']) for step in steps] == [(3, 0), (4, 0), (4, 1), (3, 1), (3, 1)]
    assert [dt.datetime.fromisoformat(step['ts']) for step in steps[:4]] == [filed_at, moved_at, done_at, dropped_at]
    assert dt.datetime.fromisoformat(steps[-1]['ts']) > now


@pytest.mark.parametrize('routed', sorted(ROUTED_NAMES))
def test_an_initiative_cannot_take_the_name_of_a_route(client, routed):
    """Each path under `/initiatives` is reached before an initiative named for it could be read by name."""
    assert ok(client.post(INITIATIVES, json={'summary': 'It is done.', 'name': routed}), status.HTTP_422_UNPROCESSABLE_CONTENT)
    assert ok(client.patch(f'{INITIATIVES}/Ship the issue tracker', json={'name': routed}), status.HTTP_422_UNPROCESSABLE_CONTENT)


def test_the_board_counts_each_issue_in_one_state_rolls_them_up_and_counts_only_completed_issues_as_closes(client, txn_api_logged_in):
    """A canceled issue and a close at no known time are in the counts and in no window. A close's day is the reader's."""
    _, session = txn_api_logged_in

    def created(name: str, **fields) -> int:
        return wrote(
            client.post(INITIATIVES, json={'summary': 'It is done.', 'name': name, **fields}), 'initiative', status.HTTP_201_CREATED
        )['number']

    urgent, low, someday = created('Urgent board', priority=1), created('Low board', priority=4), created('Later', status='someday')
    empty = created('Empty board')

    def filed(initiative: int, title: str, **fields) -> int:
        issue = {'title': title, 'initiative': initiative, 'repo': 'zeta', **fields}
        return wrote(client.post('/issues', json=issue), 'issue', status.HTTP_201_CREATED)['number']

    blocker = filed(urgent, 'Blocker', repo='alpha')
    blocked = filed(urgent, 'Blocked', depends_on=[blocker])
    running = filed(urgent, 'Running')
    completed, older, untimed = (
        filed(urgent, 'Completed'),
        filed(urgent, 'Completed eight days ago'),
        filed(urgent, 'Completed at no known time'),
    )
    canceled = filed(urgent, 'Canceled', repo='left-out')
    filed(low, 'Low')
    filed(someday, 'Shelved')
    ok(client.post('/claims', json={'record': running, 'holder': 'claude-code/s1'}))
    ok(client.post('/claims', json={'record': urgent, 'holder': 'claude-code/s2'}))
    ok(client.patch(f'/issues/{canceled}', json={'status': 'canceled', 'status_reason': 'not needed'}))
    for number in (completed, older, untimed):
        ok(client.patch(f'/issues/{number}', json={'status': 'completed'}))
    new_york = ZoneInfo('America/New_York')
    today = dt.datetime.now(new_york).date()
    # Half past eleven at night in New York is already the next day in UTC.
    older_day = today - dt.timedelta(days=8)
    older_close = dt.datetime.combine(older_day, dt.time(23, 30), new_york)
    session.execute(update(models.Issue).where(models.Issue.number == older).values(closed_ts=older_close))
    session.execute(update(models.Issue).where(models.Issue.number == untimed).values(closed_ts=None))
    session.flush()

    board = ok(client.get(f'{INITIATIVES}/board', params={'timezone': 'America/New_York'}))
    assert (board['zone'], board['days'][-1], len(board['days'])) == ('America/New_York', today.isoformat(), 14)

    listed = ok(client.get(INITIATIVES))
    assert numbers(board['initiatives']) == numbers(listed)
    row = next(row for row in board['initiatives'] if row['number'] == urgent)
    assert (row['issue_count'], row['open_count'], row['repos']) == (7, 3, ['alpha', 'zeta'])
    assert row['claim']['holder'] == 'claude-code/s2'
    assert sum(row['states'].values()) == row['issue_count']
    assert (row['states']['running'], row['states']['blocked'], row['states']['ready']) == (1, 1, 1)
    assert (row['states']['completed'], row['states']['canceled']) == (3, 1)
    assert numbers(row['issues'])[:3] == [blocker, blocked, running]
    assert set(numbers(row['issues'])[3:]) == {completed, older, canceled, untimed}
    assert next(issue for issue in row['issues'] if issue['number'] == running)['holder'] == 'claude-code/s1'
    assert next(issue for issue in row['issues'] if issue['number'] == blocker)['effective_priority'] == 1

    closes = row['closes']
    assert (closes['last_24h'], closes['last_7d'], closes['last_30d'], sum(closes['by_day'])) == (1, 1, 2, 2)
    assert closes['by_day'][-1] == closes['by_day'][board['days'].index(older_day.isoformat())] == 1
    assert closes['last_closed_ts'] is not None

    tiers = {tier['priority']: tier for tier in board['tiers']}
    assert [tier['priority'] for tier in board['tiers']] == [1, 2, 3, 4, 0]
    assert (tiers[1]['initiative_count'], tiers[1]['issue_count'], tiers[1]['closes']['last_30d']) == (1, 7, 2)
    assert sum(tier['initiative_count'] for tier in board['tiers']) == board['totals']['initiative_count'] == len(listed)
    assert sum(tier['issue_count'] for tier in board['tiers']) == board['totals']['issue_count']

    cuts = {cut['repo']: cut for cut in row['by_repo']}
    assert list(cuts) == ['zeta', 'alpha', 'left-out']
    assert (cuts['zeta']['issue_count'], cuts['zeta']['open_count'], cuts['zeta']['closes']['last_30d']) == (5, 2, 2)
    assert sum(cut['issue_count'] for cut in row['by_repo']) == row['issue_count']
    empty_row = next(row for row in board['initiatives'] if row['number'] == empty)
    assert [(cut['repo'], cut['issue_count']) for cut in empty_row['by_repo']] == [(None, 0)]
    repos = {rollup['repo']: rollup for rollup in board['repos']}
    assert (repos['zeta']['initiative_count'], repos['zeta']['issue_count'], repos['zeta']['open_count']) == (2, 6, 3)
    assert repos[None]['initiative_count'] >= 1
    assert sum(rollup['issue_count'] for rollup in board['repos']) == board['totals']['issue_count']
    assert board['repos'][-1]['repo'] is None

    shelved = ok(client.get(f'{INITIATIVES}/board', params={'status': 'someday'}))
    assert numbers(shelved['initiatives']) == [someday]
    assert shelved['initiatives'][0]['issues'][0]['state'] == 'shelved'
    assert ok(client.get(f'{INITIATIVES}/board', params={'status': 'nonsense'}), status.HTTP_422_UNPROCESSABLE_CONTENT)


def test_the_board_asks_the_same_queries_however_many_initiatives_it_answers(client, txn_api_logged_in):
    """A poll every 15 seconds pays each query four times a minute, so a query per initiative is the failure this pins."""
    _, session = txn_api_logged_in

    def filed_initiatives(count: int, prefix: str) -> None:
        for at in range(count):
            number = wrote(
                client.post(INITIATIVES, json={'summary': 'It is done.', 'name': f'{prefix} {at}'}), 'initiative', status.HTTP_201_CREATED
            )['number']
            ok(client.post('/issues', json={'title': f'{prefix} {at} issue', 'initiative': number}), status.HTTP_201_CREATED)

    def queries_to_read(path: str) -> int:
        statements: list[str] = []

        def asked(_conn, _cursor, statement, *_rest):
            statements.append(statement)

        engine = session.get_bind()
        event.listen(engine, 'before_cursor_execute', asked)
        try:
            ok(client.get(f'{INITIATIVES}/{path}'))
        finally:
            event.remove(engine, 'before_cursor_execute', asked)
        return len(statements)

    filed_initiatives(1, 'First')
    board, text = queries_to_read('board'), queries_to_read('board/text')
    filed_initiatives(5, 'More')
    assert (queries_to_read('board'), queries_to_read('board/text')) == (board, text)


def test_the_board_text_holds_each_summary_and_description_and_an_issue_excerpt_of_its_first_paragraph_of_prose(client):
    long_paragraph = 'word ' * 100
    marked = (
        '## What this is\n\n```text\nnot prose\n\nstill not prose\n```\n\n'
        '- **Bold** with `code` and a [link](https://example.com/a), *emphasis* and a *.go glob\ncarrying on.\n\nSecond paragraph.'
    )
    number = wrote(
        client.post(INITIATIVES, json={'summary': 'It is done.', 'name': 'Text', 'description': 'Whole.'}),
        'initiative',
        status.HTTP_201_CREATED,
    )['number']

    def filed(title: str, **fields) -> int:
        return wrote(client.post('/issues', json={'title': title, 'initiative': number, **fields}), 'issue', status.HTTP_201_CREATED)[
            'number'
        ]

    with_text, bare, long = filed('Marked', description=marked), filed('Bare'), filed('Long', description=long_paragraph)

    text = ok(client.get(f'{INITIATIVES}/board/text'))
    row = next(row for row in text['initiatives'] if row['number'] == number)
    assert (row['summary'], row['description']) == ('It is done.', 'Whole.')
    first, second, third = row['issues']
    assert first == {'number': with_text, 'excerpt': 'Bold with code and a link, emphasis and a *.go glob carrying on.'}
    assert second == {'number': bare, 'excerpt': None}
    assert third['number'] == long
    assert third['excerpt'].endswith('word…')
    assert len(third['excerpt']) <= 301


def test_a_summary_is_one_short_required_line_an_edit_can_change_and_never_clear(client):
    for refused in ({}, {'summary': ''}, {'summary': 'one\ntwo'}, {'summary': 'x' * 241}):
        ok(client.post(INITIATIVES, json={'name': 'Refused', **refused}), status.HTTP_422_UNPROCESSABLE_CONTENT)

    number = wrote(client.post(INITIATIVES, json={'name': 'Kept', 'summary': '  It is done.  '}), 'initiative', status.HTTP_201_CREATED)[
        'number'
    ]
    assert ok(client.get(f'{INITIATIVES}/{number}'))['summary'] == 'It is done.'
    changed = wrote(client.patch(f'{INITIATIVES}/{number}', json={'summary': 'Still done.'}), 'initiative', status.HTTP_200_OK)
    assert changed['summary'] == 'Still done.'
    ok(client.patch(f'{INITIATIVES}/{number}', json={'summary': None}), status.HTTP_422_UNPROCESSABLE_CONTENT)
