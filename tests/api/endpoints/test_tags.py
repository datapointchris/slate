import pytest
from fastapi import status

from tests.util import show_status_and_response

TAGS = '/tags'


def ok(response, code=status.HTTP_200_OK):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()


def created(client, path, json) -> dict:
    return ok(client.post(path, json=json), status.HTTP_201_CREATED)


@pytest.fixture
def client(txn_api_logged_in):
    client, _ = txn_api_logged_in
    created(client, TAGS, {'name': 'security', 'description': 'Credentials, roles and what an attacker reaches'})
    created(client, TAGS, {'name': 'performance', 'description': 'How fast a read answers'})
    return client


def numbers(rows: list[dict]) -> list[int]:
    return [row['number'] for row in rows]


def test_an_issue_reads_its_initiatives_tags_and_every_list_narrows_on_them(client):
    initiative = created(client, '/initiatives', {'name': 'Harden the servers', 'summary': 'No plaintext secret is left'})['initiative']
    inside = created(client, '/issues', {'title': 'Rotate the keys', 'initiative': initiative['number'], 'tags': ['performance']})['issue']
    created(client, '/issues', {'title': 'Elsewhere'})
    idea = created(client, '/ideas', {'title': 'Scan images', 'tags': ['security']})['idea']

    assert ok(client.patch(f'/initiatives/{initiative["number"]}', json={'tags': ['security']}))['initiative']['tags'] == ['security']

    listed = ok(client.get('/issues', params={'tag': 'security'}))
    assert numbers(listed) == [inside['number']]
    assert (listed[0]['tags'], listed[0]['effective_tags']) == (['performance'], ['performance', 'security'])
    assert numbers(ok(client.get('/initiatives', params={'tag': 'security'}))) == [initiative['number']]
    assert numbers(ok(client.get('/ideas', params={'tag': 'security'}))) == [idea['number']]
    queued = ok(client.get('/queue', params={'tag': 'security'}))['queue']
    assert sorted(row['number'] for row in queued) == sorted([inside['number'], idea['number']])


def test_a_tag_nobody_made_is_refused_naming_the_tags_that_exist(client):
    response = client.post('/ideas', json={'title': 'Faster boards', 'tags': ['perf']})
    assert response.status_code == status.HTTP_422_UNPROCESSABLE_CONTENT, show_status_and_response(response)
    message = response.json()['message']
    assert "Unknown tag 'perf'" in message
    assert 'performance, security' in message
    assert 'Near what was typed: performance' in message
    assert ok(client.get('/ideas')) == []


def test_a_rename_keeps_every_carrier_and_a_delete_names_each_it_came_off(client):
    issue = created(client, '/issues', {'title': 'Cache the board', 'tags': ['performance']})['issue']
    closed = created(client, '/issues', {'title': 'Closed one', 'tags': ['performance']})['issue']
    ok(client.patch(f'/issues/{closed["number"]}', json={'status': 'completed'}))

    def taggings(number: int) -> list[dict]:
        return [row['referenced_names'] for row in ok(client.get(f'/records/{number}/changes')) if row['table_name'] == 'record_tags']

    renamed = ok(client.patch(f'{TAGS}/performance', json={'name': 'speed'}))['tag']
    assert (renamed['name'], renamed['open_record_count'], renamed['open_issue_count']) == ('speed', 1, 1)
    assert ok(client.get(f'/issues/{issue["number"]}'))['tags'] == ['speed']
    assert taggings(issue['number']) == [{'tag_id': {'new': 'speed'}}]
    assert client.patch(f'{TAGS}/speed', json={'name': 'security'}).status_code == status.HTTP_409_CONFLICT

    deleted = ok(client.delete(f'{TAGS}/speed'))
    assert [effect['record'] for effect in deleted['side_effects']] == [issue['number'], closed['number']]
    assert ok(client.get(f'/issues/{issue["number"]}'))['tags'] == []
    assert [tag['name'] for tag in ok(client.get(TAGS))] == ['security']
    assert taggings(issue['number']) == [{'tag_id': {'old': 'speed'}}, {'tag_id': {'new': 'speed'}}]


def test_a_tag_body_refuses_a_field_it_does_not_have(client):
    response = client.post(TAGS, json={'name': 'agents', 'description': 'Agent sessions', 'descripton': 'typo'})
    assert response.status_code == status.HTTP_422_UNPROCESSABLE_CONTENT, show_status_and_response(response)


def test_a_promoted_idea_hands_its_tags_to_the_initiative(client):
    idea = created(client, '/ideas', {'title': 'Sign every image', 'tags': ['security']})['idea']
    promoted = ok(client.post(f'/ideas/{idea["number"]}/promote', json={'summary': 'Every image is signed'}))['idea']
    assert promoted['initiative']['tags'] == ['security']
