import datetime as dt
import uuid

import pytest
from fastapi import status

from slate import models
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
    initiative = ok(client.get('/initiatives/Ship the issue tracker'))['number']
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


def test_the_changes_read_every_write_to_an_issue_and_its_rows_with_imported_history_beside(client, txn_api_logged_in):
    _, session = txn_api_logged_in
    issue = ok(client.post('/issues', json={'title': 'Read the changes'}), status.HTTP_201_CREATED)['issue']
    number, issue_id = issue['number'], issue['id']
    ok(client.patch(f'/issues/{number}', json={'title': 'Read every change'}))
    ok(client.post('/comments', json={'record': number, 'body': 'Looks right'}), status.HTTP_201_CREATED)
    ok(client.post('/claims', json={'record': number, 'holder': 'agent-a'}))
    ok(client.post(f'/claims/{number}/wait', json={'holder': 'agent-a', 'waiting_on': 'review'}))
    ok(client.patch(f'/issues/{number}', json={'status': 'completed'}))

    imported = dt.datetime(2026, 10, 20, tzinfo=dt.UTC)
    comment_id, elsewhere = str(uuid.uuid4()), str(uuid.uuid4())
    session.add_all(
        models.RecordChange(changed_ts=imported, actor='person@example.com', table_name=table, record_id=key, operation=op, changes=changes)
        for table, key, op, changes in (
            ('issues', issue_id, 'update', {'claimed_by': {'old': None, 'new': 'person@example.com'}}),
            ('issue_comments', comment_id, 'insert', {'id': {'new': comment_id}, 'issue_id': {'new': issue_id}, 'body': {'new': 'Copied'}}),
            ('issue_comments', comment_id, 'update', {'body': {'old': 'Copied', 'new': 'Copied, edited'}}),
            ('issue_comments', elsewhere, 'insert', {'id': {'new': elsewhere}, 'issue_id': {'new': str(uuid.uuid4())}}),
            ('issue_label_assignments', f'{issue_id},7', 'delete', {'issue_id': {'old': issue_id}, 'label_id': {'old': 7}}),
        )
    )
    session.flush()

    changes = ok(client.get(f'/records/{number}/changes'))
    assert [(row['table_name'], row['operation']) for row in changes] == [
        ('worklogs', 'update'),
        ('claim_waits', 'update'),
        ('issues', 'update'),
        ('claims', 'update'),
        ('claim_waits', 'insert'),
        ('worklogs', 'insert'),
        ('claims', 'insert'),
        ('comments', 'insert'),
        ('issues', 'update'),
        ('issues', 'insert'),
        ('records', 'insert'),
        ('issue_label_assignments', 'delete'),
        ('issue_comments', 'update'),
        ('issue_comments', 'insert'),
        ('issues', 'update'),
    ]
    assert changes[2]['changes']['status'] == {'old': 'open', 'new': 'completed'}
    assert changes[-1]['actor'] == 'person@example.com'
    assert {row['table_name'] for row in changes if row['is_own_row']} == {'issues', 'records'}
    assert ok(client.get(f'/records/{number}/changes', params={'limit': 1})) == changes[:1]


def test_an_ideas_changes_hold_the_initiative_its_promotion_created_and_nothing_after(client):
    idea = ok(client.post('/ideas', json={'title': 'Promote me'}), status.HTTP_201_CREATED)['idea']['number']
    initiative = ok(client.post(f'/ideas/{idea}/promote', json={'summary': 'It is done.'}))['idea']['initiative']['number']
    ok(client.patch(f'/initiatives/{initiative}', json={'description': 'Its own write'}))

    changes = ok(client.get(f'/records/{idea}/changes'))
    assert [(row['table_name'], row['operation']) for row in changes if row['table_name'] == 'initiatives'] == [('initiatives', 'insert')]


def test_a_search_reads_every_kind_closed_ones_included_and_unclosed_first(client):
    idea = ok(client.post('/ideas', json={'title': 'Quarry the backlog'}), status.HTTP_201_CREATED)['idea']['number']
    initiative = ok(client.post('/initiatives', json={'summary': 'It is done.', 'name': 'Quarry ideas'}), status.HTTP_201_CREATED)[
        'initiative'
    ]['number']
    closed = ok(client.post('/issues', json={'title': 'Old quarry', 'priority': 1}), status.HTTP_201_CREATED)['issue']['number']
    ok(client.patch(f'/issues/{closed}', json={'status': 'completed'}))
    urgent = ok(client.post('/issues', json={'title': 'A', 'description': 'quarry it', 'priority': 1}), status.HTTP_201_CREATED)
    urgent = urgent['issue']['number']

    found = [(row['kind'], row['number']) for row in ok(client.get('/records', params={'search': 'QUARRY'}))]
    assert found == [('issue', urgent), ('initiative', initiative), ('idea', idea), ('issue', closed)]
    assert ok(client.get('/records', params={'search': 'quarry', 'kind': 'idea'}))[0]['number'] == idea


def test_a_number_finds_its_own_record_first_closed_or_not_then_the_records_citing_it(client):
    named = ok(client.post('/issues', json={'title': 'The six audits start'}), status.HTTP_201_CREATED)['issue']['number']
    ok(client.patch(f'/issues/{named}', json={'status': 'completed'}))
    citing = ok(client.post('/initiatives', json={'summary': f'Nothing runs before {named}.', 'name': 'Audits'}), status.HTTP_201_CREATED)
    citing = citing['initiative']['number']
    for typed in (str(named), f'#{named}'):
        found = [row['number'] for row in ok(client.get('/records', params={'search': typed}))]
        assert found[0] == named and citing in found, typed


def test_a_number_read_as_the_wrong_kind_names_the_kind_that_holds_it(client):
    initiative = ok(client.get('/initiatives/Ship the issue tracker'))['number']
    refused = ok(client.get(f'/issues/{initiative}'), status.HTTP_404_NOT_FOUND)
    assert refused['message'] == f'issue {initiative} not found: #{initiative} is an initiative'
