import pytest
from fastapi import status

from tests.session_database import insert_test_data_transactional
from tests.util import show_status_and_response

IDEAS = '/ideas'
INITIATIVES = '/issues/initiatives'


@pytest.fixture
def client(txn_api_logged_in):
    client, session = txn_api_logged_in
    insert_test_data_transactional(session, 'issues', 'issue_initiatives')
    return client


def ok(response, code=status.HTTP_200_OK):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()


def new_idea(client, **fields) -> dict:
    return ok(client.post(IDEAS, json={'title': 'Run review rounds on a schedule', **fields}), status.HTTP_201_CREATED)['idea']


def test_an_idea_with_two_rows_linked_shows_both_as_evidence(client):
    idea = new_idea(client)
    link = {'refs': ['inbox:4102', 'inbox:4117'], 'note': 'Both ask for it', 'linked_by': 'claude-code/a1b2'}
    ok(client.post(f'{IDEAS}/{idea["number"]}/evidence', json=link))
    again = ok(client.post(f'{IDEAS}/{idea["number"]}/evidence', json={'refs': ['inbox:4117', 'https://example.com/thread/9']}))
    assert [warning['code'] for warning in again['warnings']] == ['evidence_already_linked']

    shown = ok(client.get(f'{IDEAS}/{idea["number"]}'))
    assert {row['ref']: (row['kind'], row['note']) for row in shown['evidence']} == {
        'inbox:4102': ('key', 'Both ask for it'),
        'inbox:4117': ('key', 'Both ask for it'),
        'https://example.com/thread/9': ('url', None),
    }


def test_promoting_an_idea_creates_an_initiative_naming_it_and_the_idea_shows_it(client):
    idea = new_idea(client, priority=2, description='So no round waits on a person to start it')
    promoted = ok(client.post(f'{IDEAS}/{idea["number"]}/promote', json={}))
    initiative = promoted['idea']['initiative']
    assert [(effect['code'], effect['record']) for effect in promoted['side_effects']] == [('initiative_created', initiative['number'])]
    assert (initiative['name'], initiative['priority'], initiative['status']) == ('Run review rounds on a schedule', 2, 'active')
    assert initiative['idea'] == {'number': idea['number'], 'kind': 'idea', 'title': idea['title'], 'status': None}
    assert ok(client.get(f'{INITIATIVES}/{initiative["number"]}'))['idea']['number'] == idea['number']

    refused = client.post(f'{IDEAS}/{idea["number"]}/promote', json={})
    assert refused.status_code == status.HTTP_409_CONFLICT, show_status_and_response(refused)

    held = ok(client.post('/claims', json={'record': idea['number'], 'holder': 'claude-code/a1b2'}))
    assert [(warning['code'], warning['record']) for warning in held['warnings']] == [('idea_promoted', initiative['number'])]
