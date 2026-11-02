import pytest
from fastapi import status

from tests.conftest import TEST_PERSON
from tests.session_database import insert_test_data_transactional
from tests.util import show_status_and_response

PLAN = '1. Read the claims model\n2. Add the documents table\n3. Write the migration\n'


@pytest.fixture
def client(txn_api_logged_in):
    client, session = txn_api_logged_in
    insert_test_data_transactional(session, 'issues', 'issue_labels', 'issue_initiatives')
    return client


def ok(response, code=status.HTTP_200_OK):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()


def bug(client) -> int:
    return next(row['number'] for row in ok(client.get('/issues')) if row['title'] == 'Ready bug with high priority')


def hold(client, record: int, session: str) -> str:
    held = ok(client.post('/claims', json={'record': record, 'holder': f'claude-code/{session}', 'session': session}))
    return held['claim']['worklog']


def test_a_plan_written_in_one_worklog_reads_back_with_a_persons_line_comment_in_the_next(client):
    record = bug(client)
    first = hold(client, record, 's1')
    written = ok(client.put(f'/documents/{record}/plan', json={'kind': 'plan', 'body': PLAN, 'session': 's1'}), status.HTTP_201_CREATED)
    assert (written['document']['version']['worklog'], written['warnings']) == (first, [])
    assert ok(client.get(f'/worklogs/{first}'))['stage'] == 'planned'

    line = {'record': record, 'document': 'plan', 'line': 2, 'body': 'Name the version column for what it counts'}
    ok(client.post('/comments', json=line), status.HTTP_201_CREATED)
    ok(client.post('/comments', json={'record': record, 'body': 'Plan reviewed'}), status.HTTP_201_CREATED)
    ok(client.post(f'/claims/{record}/release', json={'released_by': 'claude-code/s1'}))

    second = hold(client, record, 's2')
    plan = ok(client.get(f'/documents/{record}/plan'))
    (comment,) = plan['comments']
    assert (plan['version']['version'], plan['version']['body'], plan['version']['session']) == (1, PLAN, 's1')
    assert (comment['shown_line'], comment['on']['text'], comment['written_by'], comment['session']) == (
        2,
        '2. Add the documents table',
        TEST_PERSON.subject,
        None,
    )
    issue = ok(client.get(f'/issues/{record}'))
    assert [(document['address'], document['comment_count']) for document in issue['documents']] == [(f'{record}/plan', 1)]
    assert ([each['body'] for each in issue['comments']], second) == (['Plan reviewed'], f'{record}.2')


def test_a_line_comment_follows_its_line_into_a_later_version_and_sits_on_none_once_its_line_changes(client):
    record = bug(client)
    ok(client.put(f'/documents/{record}/plan', json={'kind': 'plan', 'body': PLAN}), status.HTTP_201_CREATED)
    for line in (2, 3):
        ok(
            client.post('/comments', json={'record': record, 'document': 'plan', 'line': line, 'body': f'on line {line}'}),
            status.HTTP_201_CREATED,
        )

    revised = '1. Read the claims model\n1b. Read the worklogs model\n2. Add the documents table\n3. Write the migration by hand\n'
    second = ok(client.put(f'/documents/{record}/plan', json={'body': revised}))['document']
    assert [(each['body'], each['on']['version'], each['shown_line']) for each in second['comments']] == [
        ('on line 2', 1, 3),
        ('on line 3', 1, None),
    ]
    first = ok(client.get(f'/documents/{record}/plan', params={'version': 1}))
    assert [each['shown_line'] for each in first['comments']] == [2, 3]

    unchanged = ok(client.put(f'/documents/{record}/plan', json={'body': revised}))
    assert ([each['code'] for each in unchanged['warnings']], len(unchanged['document']['versions'])) == (['document_unchanged'], 2)
