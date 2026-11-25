import pytest
from fastapi import status
from sqlalchemy import func
from sqlalchemy import select

from slate import models
from tests.conftest import TEST_PERSON
from tests.session_database import insert_test_data_transactional
from tests.util import show_status_and_response

IDEAS = '/ideas'


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


def test_promotion_changes_the_kind_and_keeps_every_row(client, txn_api_logged_in):
    _, session = txn_api_logged_in
    ok(client.post('/tags', json={'name': 'security', 'description': 'What an attacker reaches'}), status.HTTP_201_CREATED)
    idea = new_idea(client, priority=2, tags=['security'], refs=['inbox:4102'])
    number = idea['number']
    ok(client.post('/claims', json={'record': number, 'holder': 'claude-code/shape', 'step': 'shape'}))
    ok(client.post('/comments', json={'record': number, 'body': 'Worth a schedule'}), status.HTTP_201_CREATED)
    ok(client.post(f'{IDEAS}/{number}/pin', json={}))

    promoted = ok(client.post(f'{IDEAS}/{number}/promote', json={'summary': 'Rounds run on a schedule.'}))
    initiative = promoted['initiative']
    assert (initiative['number'], initiative['id'], initiative['name'], initiative['priority']) == (number, idea['id'], idea['title'], 2)
    assert [(effect['code'], effect['record']) for effect in promoted['side_effects']] == [
        ('claim_ended', number),
        ('worklog_ended', number),
    ]
    assert promoted['idea']['initiative']['number'] == number

    read = ok(client.get(f'/records/{number}'))
    shown = read['record']
    assert (read['kind'], shown['id'], shown['created_ts']) == ('initiative', idea['id'], idea['created_ts'])
    assert (shown['tags'], [row['ref'] for row in shown['refs']], shown['pinned_ts'] is not None) == (['security'], ['inbox:4102'], True)
    assert [comment['body'] for comment in shown['comments']] == ['Worth a schedule']
    assert ([claim['ending'] for claim in shown['claims']], ok(client.get(f'/worklogs/{number}.1'))['outcome']) == (
        ['promoted'],
        'promoted',
    )
    record = session.get(models.Record, number)
    assert (record.promoted_by, record.promoted_ts is not None) == (TEST_PERSON.actor, True)

    written = {(row['table_name'], row['operation']) for row in ok(client.get(f'/records/{number}/changes'))}
    assert {('ideas', 'insert'), ('ideas', 'delete'), ('records', 'update'), ('initiatives', 'insert')} <= written


def test_promotion_to_an_issue_drops_pursuits_with_a_warning(client):
    pursuits = [
        ok(client.post('/pursuits', json={'name': name, 'summary': 'It holds work'}), status.HTTP_201_CREATED)['pursuit']['number']
        for name in ('Data Warehouse', 'Personal OS')
    ]
    idea = new_idea(client, pursuits=pursuits)
    fields = {'repo': 'webapp', 'paths': ['src/rounds'], 'type': 'feature', 'acceptance': 'Rounds start on time'}
    promoted = ok(client.post(f'{IDEAS}/{idea["number"]}/promote', json={'kind': 'issue', **fields}))
    issue = promoted['issue']
    assert (issue['number'], issue['id'], issue['status']) == (idea['number'], idea['id'], 'open')
    assert {field: issue[field] for field in fields} == fields
    dropped = [(warning['record'], warning['message']) for warning in promoted['warnings'] if warning['code'] == 'pursuits_dropped']
    assert [(number, f'#{number} ' in message) for number, message in dropped] == [(number, True) for number in pursuits]
    assert ok(client.get(f'/records/{idea["number"]}'))['kind'] == 'issue'


def test_deleting_an_idea_takes_its_refs_claims_and_pin(client, txn_api_logged_in):
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
