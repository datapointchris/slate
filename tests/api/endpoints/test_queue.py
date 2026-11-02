"""`GET /queue`: every kind of open work by priority, each row's step, and held work apart."""

import pytest
from fastapi import status

from tests.util import show_status_and_response

QUEUE = '/queue'


@pytest.fixture
def client(txn_api_logged_in):
    return txn_api_logged_in[0]


def ok(response, code=status.HTTP_200_OK):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()


def issue(client, title: str, **fields) -> int:
    return ok(client.post('/issues', json={'title': title, **fields}), status.HTTP_201_CREATED)['issue']['number']


def initiative(client, name: str, **fields) -> int:
    return ok(client.post('/initiatives', json={'name': name, **fields}), status.HTTP_201_CREATED)['initiative']['number']


def hold(client, record: int, holder: str) -> None:
    ok(client.post('/claims', json={'record': record, 'holder': holder}))


def rows(client) -> list[tuple[str, str, int]]:
    return [(row['title'], row['step'], row['priority']) for row in ok(client.get(QUEUE))['queue']]


def test_every_kind_sorts_by_priority_with_its_step_and_held_work_listed_apart(client):
    ok(client.post('/ideas', json={'title': 'Schedule review rounds', 'priority': 2}), status.HTTP_201_CREATED)
    initiative(client, 'Plan the importer', priority=1)
    issue(client, 'Pick a storage format', type='decision', priority=3)
    issue(client, 'Write the parser', priority=4)
    hold(client, issue(client, 'Fix the flaky test', priority=1), 'agent-a')

    assert rows(client) == [
        ('Plan the importer', 'plan', 1),
        ('Schedule review rounds', 'discuss', 2),
        ('Pick a storage format', 'decide', 3),
        ('Write the parser', 'build', 4),
    ]
    assert [(row['title'], row['holder']) for row in ok(client.get(QUEUE))['claimed']] == [('Fix the flaky test', 'agent-a')]


def test_each_initiatives_issues_run_together_and_move_with_it(client):
    """Filed interleaved, each initiative's issues still run together, in the order the initiatives rank."""
    importer = initiative(client, 'Ship the importer', priority=1)
    exporter = initiative(client, 'Ship the exporter', priority=1)
    for step in (1, 2):
        issue(client, f'Import stage {step}', initiative=importer)
        issue(client, f'Export stage {step}', initiative=exporter)
    issue(client, 'Patch the outage', priority=1)

    importer_first = ['Import stage 1', 'Import stage 2', 'Export stage 1', 'Export stage 2', 'Patch the outage']
    assert [title for title, _, _ in rows(client)] == importer_first

    ok(client.post(f'/initiatives/{exporter}/rank', json={'before': importer}))
    exporter_first = ['Export stage 1', 'Export stage 2', 'Import stage 1', 'Import stage 2', 'Patch the outage']
    assert [title for title, _, _ in rows(client)] == exporter_first


def test_ideas_promote_after_ideas_reorder_keeps_the_place_the_reorder_gave(client):
    first, _, moved = (
        ok(client.post('/ideas', json={'title': title, 'priority': 2}), status.HTTP_201_CREATED)['idea']['number']
        for title in ('Schedule review rounds', 'Mirror the backups', 'Trim the test suite')
    )
    ok(client.post(f'/ideas/{moved}/rank', json={'before': first}))
    ok(client.post(f'/ideas/{moved}/promote', json={}))
    assert rows(client) == [
        ('Trim the test suite', 'plan', 2),
        ('Schedule review rounds', 'discuss', 2),
        ('Mirror the backups', 'discuss', 2),
    ]


def test_a_decision_the_work_waits_on_is_decided_at_that_works_priority(client):
    decision = issue(client, 'Choose the wire format', type='decision')
    issue(client, 'Ship the sync', priority=2, depends_on=[decision])
    assert rows(client) == [('Choose the wire format', 'decide', 2)]


def test_a_ready_issue_names_each_held_issue_changing_the_same_code(client):
    held = issue(client, 'Rework the router', repo='webapp', paths=['src/api'])
    hold(client, held, 'agent-a')
    issue(client, 'Log each request', repo='webapp', paths=['src/api/main.py'])

    (row,) = ok(client.get(QUEUE))['queue']
    assert [(overlap['number'], overlap['holder'], overlap['paths']) for overlap in row['overlaps']] == [(held, 'agent-a', ['src/api'])]
