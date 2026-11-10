from fastapi import status

from tests.util import show_status_and_response


def ok(response, code=status.HTTP_200_OK):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()


def created(client, path, json) -> dict:
    return ok(client.post(path, json=json), status.HTTP_201_CREATED)


def numbers(rows: list[dict]) -> list[int]:
    return [row['number'] for row in rows]


def a_direction(client, name='Data Warehouse') -> dict:
    return created(client, '/directions', {'name': name, 'summary': 'All data flows through the warehouse'})['direction']


def test_an_initiative_placed_in_a_direction_lends_it_to_its_issues_and_every_list_narrows_on_it(txn_api_logged_in):
    client, _ = txn_api_logged_in
    direction = a_direction(client)
    named = {'number': direction['number'], 'name': 'Data Warehouse'}
    initiative = created(client, '/initiatives', {'name': 'Load the events', 'summary': 'Every event lands in a table'})['initiative']
    inside = created(client, '/issues', {'title': 'Write the loader', 'initiative': initiative['number']})['issue']
    created(client, '/issues', {'title': 'Elsewhere'})
    idea = created(client, '/ideas', {'title': 'Model the sessions', 'directions': [direction['number']]})['idea']

    edited = ok(client.patch(f'/initiatives/{initiative["number"]}', json={'directions': [direction['number']]}))['initiative']
    assert edited['directions'] == [named]
    assert ok(client.get(f'/issues/{inside["number"]}'))['directions'] == [named]

    for ref in (direction['number'], 'Data Warehouse'):
        assert numbers(ok(client.get('/issues', params={'direction': ref}))) == [inside['number']]
    assert numbers(ok(client.get('/initiatives', params={'direction': direction['number']}))) == [initiative['number']]
    assert numbers(ok(client.get('/ideas', params={'direction': direction['number']}))) == [idea['number']]
    queued = ok(client.get('/queue', params={'direction': direction['number']}))
    assert sorted(numbers(queued['queue'])) == sorted([inside['number'], idea['number']])
    board = ok(client.get('/initiatives/board'))['initiatives']
    assert [row['directions'] for row in board] == [[named]]

    listed = ok(client.get('/directions'))
    assert [(row['name'], numbers(row['initiatives']), numbers(row['ideas'])) for row in listed] == [
        ('Data Warehouse', [initiative['number']], [idea['number']])
    ]
    shown = ok(client.get(f'/records/{direction["number"]}'))
    assert (shown['kind'], shown['record']['name']) == ('direction', 'Data Warehouse')


def test_a_number_naming_no_direction_is_refused_naming_the_active_ones(txn_api_logged_in):
    client, _ = txn_api_logged_in
    direction = a_direction(client)
    initiative = created(client, '/initiatives', {'name': 'Load the events', 'summary': 'Every event lands in a table'})['initiative']

    response = client.patch(f'/initiatives/{initiative["number"]}', json={'directions': [initiative['number']]})
    assert response.status_code == status.HTTP_422_UNPROCESSABLE_CONTENT, show_status_and_response(response)
    assert f'#{initiative["number"]} names no direction' in response.json()['message']
    assert f'#{direction["number"]} Data Warehouse' in response.json()['message']

    filtered = client.get('/issues', params={'direction': 'Data Warehose'})
    assert filtered.status_code == status.HTTP_422_UNPROCESSABLE_CONTENT, show_status_and_response(filtered)
    assert 'Near what was typed: Data Warehouse' in filtered.json()['message']


def test_a_direction_holds_no_claim_and_no_place_in_the_queue(txn_api_logged_in):
    client, _ = txn_api_logged_in
    direction = a_direction(client)

    held = client.post('/claims', json={'record': direction['number'], 'holder': 'claude-code/test'})
    assert held.status_code == status.HTTP_422_UNPROCESSABLE_CONTENT, show_status_and_response(held)
    assert ok(client.get('/claims', params={'record': direction['number']})) == []
    assert direction['number'] not in numbers(ok(client.get('/queue'))['queue'])


def test_a_promoted_idea_hands_its_directions_to_the_initiative(txn_api_logged_in):
    client, _ = txn_api_logged_in
    direction = a_direction(client)
    idea = created(client, '/ideas', {'title': 'Model the sessions', 'directions': [direction['number']]})['idea']
    promoted = ok(client.post(f'/ideas/{idea["number"]}/promote', json={'summary': 'Sessions are a table'}))['idea']
    assert promoted['initiative']['directions'] == [{'number': direction['number'], 'name': 'Data Warehouse'}]


def test_a_membership_reads_as_a_change_of_both_records_under_the_directions_name(txn_api_logged_in):
    client, _ = txn_api_logged_in
    direction = a_direction(client)
    initiative = created(client, '/initiatives', {'name': 'Load the events', 'summary': 'Every event lands in a table'})['initiative']
    ok(client.patch(f'/initiatives/{initiative["number"]}', json={'directions': [direction['number']]}))
    ok(client.patch(f'/directions/{direction["number"]}', json={'name': 'Warehouse'}))

    def joins(number: int) -> list[dict]:
        return [row['referenced_names'] for row in ok(client.get(f'/records/{number}/changes')) if row['table_name'] == 'record_directions']

    assert joins(initiative['number']) == joins(direction['number']) == [{'direction_id': {'new': 'Warehouse'}}]


def test_a_finished_direction_hides_and_takes_no_new_work(txn_api_logged_in):
    client, _ = txn_api_logged_in
    direction = a_direction(client)
    inside = created(
        client, '/initiatives', {'name': 'Load the events', 'summary': 'Every event lands', 'directions': [direction['number']]}
    )
    refused = client.patch(f'/directions/{direction["number"]}', json={'status': 'dropped'})
    assert refused.status_code == status.HTTP_422_UNPROCESSABLE_CONTENT, show_status_and_response(refused)

    completed = ok(client.patch('/directions/Data Warehouse', json={'status': 'completed'}))['direction']
    assert completed['closed_ts'] is not None
    assert ok(client.get('/directions')) == []
    assert numbers(ok(client.get('/directions', params={'status': 'all'}))) == [direction['number']]

    # A placing by a stale number, such as an unattended shaping run's, is refused, and a member keeps its place.
    placed = client.post('/initiatives', json={'name': 'Late', 'summary': 'Too late', 'directions': [direction['number']]})
    assert placed.status_code == status.HTTP_409_CONFLICT, show_status_and_response(placed)
    kept = {'directions': [direction['number']], 'priority': 1}
    assert ok(client.patch(f'/initiatives/{inside["initiative"]["number"]}', json=kept))['initiative']['directions'] != []
