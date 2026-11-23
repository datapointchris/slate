import pytest
from fastapi import status
from sqlalchemy import func
from sqlalchemy import select

from slate import models
from tests.session_database import insert_test_data_transactional
from tests.util import show_status_and_response

IDEAS = '/ideas'
INITIATIVES = '/initiatives'


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


def test_an_idea_with_two_rows_linked_shows_both_as_refs(client):
    idea = new_idea(client)
    link = {'refs': ['inbox:4102', 'inbox:4117'], 'note': 'Both ask for it', 'linked_by': 'claude-code/a1b2'}
    ok(client.post(f'{IDEAS}/{idea["number"]}/refs', json=link))
    again = ok(client.post(f'{IDEAS}/{idea["number"]}/refs', json={'refs': ['inbox:4117', 'https://example.com/thread/9']}))
    assert [warning['code'] for warning in again['warnings']] == ['ref_already_linked']

    shown = ok(client.get(f'{IDEAS}/{idea["number"]}'))
    assert {row['ref']: (row['kind'], row['note']) for row in shown['refs']} == {
        'inbox:4102': ('key', 'Both ask for it'),
        'inbox:4117': ('key', 'Both ask for it'),
        'https://example.com/thread/9': ('url', None),
    }


def test_promoting_an_idea_a_session_researched_under_a_claim_creates_its_initiative_and_ends_the_claim(client):
    idea = new_idea(client, priority=2, description='So no round waits on a person to start it')
    ok(client.post('/claims', json={'record': idea['number'], 'holder': 'claude-code/research'}))
    promoted = ok(client.post(f'{IDEAS}/{idea["number"]}/promote', json={'summary': 'It is done.'}))
    initiative = promoted['idea']['initiative']
    assert [(effect['code'], effect['record']) for effect in promoted['side_effects']] == [
        ('initiative_created', initiative['number']),
        ('claim_ended', idea['number']),
        ('worklog_ended', idea['number']),
    ]
    assert promoted['idea']['claim'] is None
    assert (initiative['name'], initiative['priority'], initiative['status']) == ('Run review rounds on a schedule', 2, 'active')
    assert initiative['idea'] == {'number': idea['number'], 'kind': 'idea', 'title': idea['title'], 'status': None}
    assert ok(client.get(f'{INITIATIVES}/{initiative["number"]}'))['idea']['number'] == idea['number']

    refused = client.post(f'{IDEAS}/{idea["number"]}/promote', json={'summary': 'It is done.'})
    assert refused.status_code == status.HTTP_409_CONFLICT, show_status_and_response(refused)

    held = ok(client.post('/claims', json={'record': idea['number'], 'holder': 'claude-code/a1b2'}))
    assert [(warning['code'], warning['record']) for warning in held['warnings']] == [('idea_promoted', initiative['number'])]


def test_deleting_an_idea_takes_its_refs_claims_and_pin_and_a_promoted_idea_is_refused(client, txn_api_logged_in):
    _, session = txn_api_logged_in
    idea = new_idea(client)
    number = idea['number']
    ok(client.post(f'{IDEAS}/{number}/refs', json={'refs': ['inbox:4102']}))
    ok(client.post('/claims', json={'record': number, 'holder': 'claude-code/a1b2'}))
    ok(client.post(f'{IDEAS}/{number}/pin', json={}))

    answer = ok(client.delete(f'{IDEAS}/{number}'))
    assert (answer['deleted']['number'], answer['deleted']['kind'], answer['deleted']['title']) == (number, 'idea', idea['title'])
    assert [(effect['code'], effect['record']) for effect in answer['side_effects']] == [
        ('claims_deleted', number),
        ('worklogs_deleted', number),
    ]
    assert [(warning['code'], warning['record']) for warning in answer['warnings']] == [('deleted_anothers_claim', number)]
    assert client.get(f'{IDEAS}/{number}').status_code == status.HTTP_404_NOT_FOUND
    assert session.scalar(select(func.count()).select_from(models.RecordRef).where(models.RecordRef.record_number == number)) == 0
    assert session.scalar(select(func.count()).select_from(models.Claim).where(models.Claim.record_number == number)) == 0
    assert session.get(models.Record, number) is None

    promoted = new_idea(client, title='Rank the queue by focus')
    initiative = ok(client.post(f'{IDEAS}/{promoted["number"]}/promote', json={'summary': 'It is done.'}))['idea']['initiative']
    refused = client.delete(f'{IDEAS}/{promoted["number"]}')
    assert refused.status_code == status.HTTP_409_CONFLICT, show_status_and_response(refused)
    assert f'initiative #{initiative["number"]}' in refused.json()['message']
    assert ok(client.get(f'{IDEAS}/{promoted["number"]}'))['initiative']['number'] == initiative['number']
