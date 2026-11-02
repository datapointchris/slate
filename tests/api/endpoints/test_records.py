import pytest
from fastapi import status

from tests.session_database import insert_test_data_transactional
from tests.util import show_status_and_response


@pytest.fixture
def client(txn_api_logged_in):
    client, session = txn_api_logged_in
    insert_test_data_transactional(session, 'issues', 'issue_initiatives')
    return client


def ok(response, code=status.HTTP_200_OK):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()


def test_the_record_read_answers_each_kind_with_its_claim_history(client):
    idea = ok(client.post('/ideas', json={'title': 'Run review rounds on a schedule'}), status.HTTP_201_CREATED)['idea']['number']
    initiative = ok(client.get('/issues/initiatives/Ship the issue tracker'))['number']
    issue = next(row['number'] for row in ok(client.get('/issues')) if row['title'] == 'Ready bug with high priority')
    ok(client.post('/claims', json={'record': idea, 'holder': 'agent-a'}))
    ok(client.post(f'/claims/{idea}/release', json={'released_by': 'agent-a'}))

    read = {number: ok(client.get(f'/records/{number}')) for number in (idea, initiative, issue)}
    assert [(answer['kind'], answer['record']['number']) for answer in read.values()] == [
        ('idea', idea),
        ('initiative', initiative),
        ('issue', issue),
    ]
    assert [(claim['holder'], claim['ending']) for claim in read[idea]['record']['claims']] == [('agent-a', 'released')]
    assert read[initiative]['record']['claims'] == []
    ok(client.get('/records/999999'), status.HTTP_404_NOT_FOUND)
