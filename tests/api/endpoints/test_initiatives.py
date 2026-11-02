import pytest
from fastapi import status

from slate import schemas
from tests.session_database import insert_test_data_transactional
from tests.util import show_status_and_response

from .crud_test import ApiCrudTester

INITIATIVES = '/initiatives'

NEW_INITIATIVE = schemas.InitiativeCreate(name='Move dev items into issues', priority=1)


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
    ok(client.post(INITIATIVES, json={'name': 'Urgent cleanup', 'priority': 1}), status.HTTP_201_CREATED)
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
    ok(client.post(INITIATIVES, json={'name': 'Started last'}), status.HTTP_201_CREATED)
    assert names(ok(client.get(INITIATIVES, params={'sort': sort}))) == expected


def test_someday_initiatives_sit_between_the_active_and_the_finished(client):
    ok(client.post(INITIATIVES, json={'name': 'Urgent idea', 'status': 'someday', 'priority': 1}), status.HTTP_201_CREATED)
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
    ok(client.post(INITIATIVES, json={'name': 'library/consumer pair'}), status.HTTP_201_CREATED)
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
    response = client.post(INITIATIVES, json={'name': 'Ship the issue tracker'})
    assert response.status_code == status.HTTP_409_CONFLICT, show_status_and_response(response)
    ok(client.post(INITIATIVES, json={'name': 'Finished migration'}), status.HTTP_201_CREATED)


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
