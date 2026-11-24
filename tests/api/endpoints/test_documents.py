import datetime as dt

import pytest
from fastapi import status

from slate import models
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


def test_a_list_names_each_documents_record_and_bounds_by_record_so_none_is_half_listed(client):
    issue = bug(client)
    initiative = next(row for row in ok(client.get('/initiatives')) if row['name'] == 'Ship the issue tracker')['number']
    ok(client.put(f'/documents/{initiative}/design', json={'kind': 'design', 'body': PLAN}), status.HTTP_201_CREATED)
    ok(client.put(f'/documents/{issue}/plan', json={'kind': 'plan', 'body': PLAN}), status.HTTP_201_CREATED)
    ok(client.put(f'/documents/{initiative}/plan', json={'kind': 'plan', 'body': PLAN}), status.HTTP_201_CREATED)

    listed = ok(client.get('/documents'))
    assert [(row['address'], row['record']['kind'], row['record']['title']) for row in listed] == [
        (f'{initiative}/plan', 'initiative', 'Ship the issue tracker'),
        (f'{issue}/plan', 'issue', 'Ready bug with high priority'),
        (f'{initiative}/design', 'initiative', 'Ship the issue tracker'),
    ]
    newest = client.get('/documents', params={'record_limit': 1})
    assert ([row['address'] for row in ok(newest)], newest.headers['X-Total-Records']) == (
        [f'{initiative}/plan', f'{initiative}/design'],
        '2',
    )
    refused = client.get('/documents', params={'record_limit': 501})
    assert '500' in ok(refused, status.HTTP_422_UNPROCESSABLE_CONTENT)['message']
    assert [(document['address'], 'record' in document) for document in ok(client.get(f'/issues/{issue}'))['documents']] == [
        (f'{issue}/plan', False)
    ]


def test_a_list_naming_no_record_stops_at_fifty_records(txn_api_logged_in):
    client, session = txn_api_logged_in
    start = dt.datetime(2026, 11, 1, tzinfo=dt.UTC)
    ideas = [models.Idea(title=f'idea {n}') for n in range(51)]
    session.add_all(ideas)
    session.flush()
    for n, idea in enumerate(ideas):
        document = models.Document(record_number=idea.number, name='research', kind='research')
        session.add(
            models.DocumentVersion(document=document, version=1, body='x', written_by='w', written_ts=start + dt.timedelta(minutes=n))
        )
    session.flush()

    listed = client.get('/documents')
    assert (len({row['record_number'] for row in ok(listed)}), listed.headers['X-Total-Records']) == (50, '51')
    assert ideas[0].number not in {row['record_number'] for row in listed.json()}


def test_slate_documents_write_design_kind_plan_is_refused_so_record_design_is_always_a_design(client):
    record = bug(client)
    ok(client.put(f'/documents/{record}/design', json={'kind': 'plan', 'body': PLAN}), status.HTTP_422_UNPROCESSABLE_CONTENT)
    assert ok(client.get('/documents', params={'record': record})) == []
