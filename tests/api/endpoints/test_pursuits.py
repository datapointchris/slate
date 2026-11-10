import datetime as dt

from fastapi import status
from sqlalchemy import update

from slate import models
from tests.util import show_status_and_response


def ok(response, code=status.HTTP_200_OK):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()


def created(client, path, json) -> dict:
    return ok(client.post(path, json=json), status.HTTP_201_CREATED)


def numbers(rows: list[dict]) -> list[int]:
    return [row['number'] for row in rows]


def a_pursuit(client, name='Data Warehouse') -> dict:
    return created(client, '/pursuits', {'name': name, 'summary': 'All data flows through the warehouse'})['pursuit']


def test_an_initiative_placed_in_a_pursuit_lends_it_to_its_issues_and_every_list_narrows_on_it(txn_api_logged_in):
    client, _ = txn_api_logged_in
    pursuit = a_pursuit(client)
    named = {'number': pursuit['number'], 'name': 'Data Warehouse'}
    initiative = created(client, '/initiatives', {'name': 'Load the events', 'summary': 'Every event lands in a table'})['initiative']
    inside = created(client, '/issues', {'title': 'Write the loader', 'initiative': initiative['number']})['issue']
    created(client, '/issues', {'title': 'Elsewhere'})
    idea = created(client, '/ideas', {'title': 'Model the sessions', 'pursuits': [pursuit['number']]})['idea']

    edited = ok(client.patch(f'/initiatives/{initiative["number"]}', json={'pursuits': [pursuit['number']]}))['initiative']
    assert edited['pursuits'] == [named]
    assert ok(client.get(f'/issues/{inside["number"]}'))['pursuits'] == [named]

    for ref in (pursuit['number'], 'Data Warehouse'):
        assert numbers(ok(client.get('/issues', params={'pursuit': ref}))) == [inside['number']]
    assert numbers(ok(client.get('/initiatives', params={'pursuit': pursuit['number']}))) == [initiative['number']]
    assert numbers(ok(client.get('/ideas', params={'pursuit': pursuit['number']}))) == [idea['number']]
    queued = ok(client.get('/queue', params={'pursuit': pursuit['number']}))
    assert sorted(numbers(queued['queue'])) == sorted([inside['number'], idea['number']])
    board = ok(client.get('/initiatives/board'))['initiatives']
    assert [row['pursuits'] for row in board] == [[named]]

    listed = ok(client.get('/pursuits'))
    assert [(row['name'], numbers(row['initiatives']), numbers(row['ideas'])) for row in listed] == [
        ('Data Warehouse', [initiative['number']], [idea['number']])
    ]
    shown = ok(client.get(f'/records/{pursuit["number"]}'))
    assert (shown['kind'], shown['record']['name']) == ('pursuit', 'Data Warehouse')


def test_a_pursuit_carries_the_command_that_measures_it_until_an_edit_clears_it(txn_api_logged_in):
    client, _ = txn_api_logged_in
    body = {'name': 'Data Warehouse', 'summary': 'All data flows through the warehouse', 'measure': 'warehouse-coverage --json'}
    pursuit = created(client, '/pursuits', body)['pursuit']
    assert pursuit['measure'] == 'warehouse-coverage --json'

    ok(client.patch(f'/pursuits/{pursuit["number"]}', json={'measure': 'warehouse-coverage --since 7d'}))
    assert [row['measure'] for row in ok(client.get('/pursuits'))] == ['warehouse-coverage --since 7d']
    assert ok(client.patch(f'/pursuits/{pursuit["number"]}', json={'measure': None}))['pursuit']['measure'] is None


def test_a_number_naming_no_pursuit_is_refused_naming_the_active_ones(txn_api_logged_in):
    client, _ = txn_api_logged_in
    pursuit = a_pursuit(client)
    initiative = created(client, '/initiatives', {'name': 'Load the events', 'summary': 'Every event lands in a table'})['initiative']

    response = client.patch(f'/initiatives/{initiative["number"]}', json={'pursuits': [initiative['number']]})
    assert response.status_code == status.HTTP_422_UNPROCESSABLE_CONTENT, show_status_and_response(response)
    assert f'#{initiative["number"]} names no pursuit' in response.json()['message']
    assert f'#{pursuit["number"]} Data Warehouse' in response.json()['message']

    filtered = client.get('/issues', params={'pursuit': 'Data Warehose'})
    assert filtered.status_code == status.HTTP_422_UNPROCESSABLE_CONTENT, show_status_and_response(filtered)
    assert 'Near what was typed: Data Warehouse' in filtered.json()['message']


def test_a_pursuit_holds_no_claim_and_no_place_in_the_queue(txn_api_logged_in):
    client, _ = txn_api_logged_in
    pursuit = a_pursuit(client)

    held = client.post('/claims', json={'record': pursuit['number'], 'holder': 'claude-code/test'})
    assert held.status_code == status.HTTP_422_UNPROCESSABLE_CONTENT, show_status_and_response(held)
    assert ok(client.get('/claims', params={'record': pursuit['number']})) == []
    assert pursuit['number'] not in numbers(ok(client.get('/queue'))['queue'])


def test_a_promoted_idea_hands_its_pursuits_to_the_initiative(txn_api_logged_in):
    client, _ = txn_api_logged_in
    pursuit = a_pursuit(client)
    idea = created(client, '/ideas', {'title': 'Model the sessions', 'pursuits': [pursuit['number']]})['idea']
    promoted = ok(client.post(f'/ideas/{idea["number"]}/promote', json={'summary': 'Sessions are a table'}))['idea']
    assert promoted['initiative']['pursuits'] == [{'number': pursuit['number'], 'name': 'Data Warehouse'}]


def test_a_membership_reads_as_a_change_of_both_records_under_the_pursuits_name(txn_api_logged_in):
    client, _ = txn_api_logged_in
    pursuit = a_pursuit(client)
    initiative = created(client, '/initiatives', {'name': 'Load the events', 'summary': 'Every event lands in a table'})['initiative']
    ok(client.patch(f'/initiatives/{initiative["number"]}', json={'pursuits': [pursuit['number']]}))
    ok(client.patch(f'/pursuits/{pursuit["number"]}', json={'name': 'Warehouse'}))

    def joins(number: int) -> list[dict]:
        return [row['referenced_names'] for row in ok(client.get(f'/records/{number}/changes')) if row['table_name'] == 'record_pursuits']

    assert joins(initiative['number']) == joins(pursuit['number']) == [{'pursuit_id': {'new': 'Warehouse'}}]


def test_a_finished_pursuit_hides_and_takes_no_new_work(txn_api_logged_in):
    client, _ = txn_api_logged_in
    pursuit = a_pursuit(client)
    inside = created(client, '/initiatives', {'name': 'Load the events', 'summary': 'Every event lands', 'pursuits': [pursuit['number']]})
    refused = client.patch(f'/pursuits/{pursuit["number"]}', json={'status': 'dropped'})
    assert refused.status_code == status.HTTP_422_UNPROCESSABLE_CONTENT, show_status_and_response(refused)

    completed = ok(client.patch('/pursuits/Data Warehouse', json={'status': 'completed'}))['pursuit']
    assert completed['closed_ts'] is not None
    assert ok(client.get('/pursuits')) == []
    assert numbers(ok(client.get('/pursuits', params={'status': 'all'}))) == [pursuit['number']]

    # A placing by a stale number, such as an unattended shaping run's, is refused, and a member keeps its place.
    placed = client.post('/initiatives', json={'name': 'Late', 'summary': 'Too late', 'pursuits': [pursuit['number']]})
    assert placed.status_code == status.HTTP_409_CONFLICT, show_status_and_response(placed)
    kept = {'pursuits': [pursuit['number']], 'priority': 1}
    assert ok(client.patch(f'/initiatives/{inside["initiative"]["number"]}', json=kept))['initiative']['pursuits'] != []


def test_the_board_rolls_up_each_pursuit_and_tag_with_closes_from_initiatives_off_it_and_its_initiative_untouched_longest(
    txn_api_logged_in,
):
    """An initiative in two pursuits counts in both, and one completed inside the window still counts its closes in its pursuit."""
    client, session = txn_api_logged_in
    warehouse, security, empty = (a_pursuit(client, name) for name in ('Data Warehouse', 'Fleet Security', 'Empty'))
    created(client, '/tags', {'name': 'security', 'description': 'Credentials, roles and what an attacker reaches'})

    def initiative(name: str, pursuits: list[dict], **fields) -> int:
        body = {'name': name, 'summary': 'It is done.', 'pursuits': [d['number'] for d in pursuits], **fields}
        return created(client, '/initiatives', body)['initiative']['number']

    def issue(initiative: int, title: str) -> int:
        return created(client, '/issues', {'title': title, 'initiative': initiative})['issue']['number']

    both = initiative('Both', [warehouse, security], tags=['security'])
    stale = initiative('Stale', [warehouse])
    finished = initiative('Finished', [security])
    unplaced = initiative('Unplaced', [])
    ok(client.patch(f'/issues/{issue(both, "Closed now")}', json={'status': 'completed'}))
    issue(stale, 'Waiting')
    ok(client.patch(f'/issues/{issue(finished, "Closed last week")}', json={'status': 'completed'}))
    ok(client.patch(f'/initiatives/{finished}', json={'status': 'completed'}))
    long_ago = dt.datetime.now(dt.UTC) - dt.timedelta(days=40)
    session.execute(update(models.Initiative).where(models.Initiative.number == stale).values(created_ts=long_ago))
    session.flush()

    board = ok(client.get('/initiatives/board'))
    touched = {row['number']: dt.datetime.fromisoformat(row['touched_ts']) for row in board['initiatives']}
    assert touched[stale] == long_ago

    def rolled(rollup: dict) -> tuple:
        untouched = rollup['untouched_longest']
        return (rollup['initiative_count'], rollup['closes']['last_30d'], untouched and untouched['number'])

    pursuits = {(rollup['pursuit'] or {}).get('number'): rollup for rollup in board['pursuits']}
    assert [rollup['pursuit'] and rollup['pursuit']['name'] for rollup in board['pursuits']] == [
        'Empty',
        'Data Warehouse',
        'Fleet Security',
        None,
    ]
    assert rolled(pursuits[empty['number']]) == (0, 0, None)
    assert rolled(pursuits[warehouse['number']]) == (2, 1, stale)
    assert sorted(pursuits[warehouse['number']]['members']) == sorted([both, stale])
    assert rolled(pursuits[security['number']]) == (1, 2, both)
    assert rolled(pursuits[None]) == (1, 0, unplaced)

    tags = {rollup['tag']: rollup for rollup in board['tags']}
    assert list(tags) == ['security', None]
    assert rolled(tags['security']) == (1, 1, both)
    assert rolled(tags[None]) == (2, 1, stale)
