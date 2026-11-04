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


def pin(client, noun: str, ref: int, **body) -> dict:
    return ok(client.post(f'/{noun}/{ref}/pin', json=body))


def test_a_pin_leads_every_priority_and_an_initiative_brings_its_issues(client):
    issue(client, 'Patch the outage', priority=1)
    free = issue(client, 'Water the plants', priority=4)
    idea = ok(client.post('/ideas', json={'title': 'Try a layout', 'priority': 4}), status.HTTP_201_CREATED)['idea']['number']
    importer = initiative(client, 'Ship the importer', priority=4)
    issue(client, 'Import stage 1', initiative=importer)
    by_priority = ['Patch the outage', 'Water the plants', 'Try a layout', 'Import stage 1']
    assert [title for title, _, _ in rows(client)] == by_priority

    assert pin(client, 'issues', free, session='s1', host='box')['issue']['pinned_session'] == 's1'
    pin(client, 'ideas', idea)
    pin(client, 'initiatives', importer)

    queue = ok(client.get(QUEUE))['queue']
    assert [row['title'] for row in queue] == ['Water the plants', 'Try a layout', 'Import stage 1', 'Patch the outage']
    assert [row['pinned'] for row in queue] == [True, True, True, False]
    assert queue[0]['record']['pinned_ts'] is not None
    assert queue[0]['record']['pinned_by'] is not None
    assert queue[2]['record']['pinned_ts'] is None
    assert ok(client.get('/initiatives'))[0]['number'] == importer

    for noun, ref in (('issues', free), ('ideas', idea), ('initiatives', importer)):
        ok(client.delete(f'/{noun}/{ref}/pin'))
    assert [title for title, _, _ in rows(client)] == by_priority


def test_a_blocker_of_a_pinned_issue_sorts_just_ahead_of_it(client):
    gated = issue(client, 'Ship it', priority=4)
    blocker = issue(client, 'Fix the build', priority=4)
    ok(client.post(f'/issues/{gated}/dependencies', json={'depends_on': blocker}), status.HTTP_201_CREATED)
    issue(client, 'Patch the outage', priority=1)

    pin(client, 'issues', gated)

    head = ok(client.get(QUEUE))['queue'][0]
    assert (head['title'], head['pinned'], head['raised_by']) == ('Fix the build', True, gated)


def test_a_pin_ends_with_the_record_and_each_ending_says_so(client):
    closing = issue(client, 'Water the plants')
    pin(client, 'issues', closing)
    closed = ok(client.patch(f'/issues/{closing}', json={'status': 'completed'}))
    assert closed['issue']['pinned_ts'] is None
    assert [effect['code'] for effect in closed['side_effects']] == ['pin_cleared']

    shelving = initiative(client, 'Ship the importer')
    pin(client, 'initiatives', shelving)
    shelved = ok(client.patch(f'/initiatives/{shelving}', json={'status': 'someday'}))
    assert shelved['initiative']['pinned_ts'] is None
    assert [effect['code'] for effect in shelved['side_effects']] == ['pin_cleared']

    idea = ok(client.post('/ideas', json={'title': 'Try a layout'}), status.HTTP_201_CREATED)['idea']['number']
    pin(client, 'ideas', idea)
    promoted = ok(client.post(f'/ideas/{idea}/promote', json={}))
    assert 'pin_carried' in [effect['code'] for effect in promoted['side_effects']]
    assert promoted['idea']['pinned_ts'] is None
    assert promoted['idea']['initiative']['pinned_ts'] is not None


def test_a_move_across_the_pin_and_a_pin_on_what_the_queue_cannot_hold_are_refused(client):
    pinned = issue(client, 'Water the plants', priority=2)
    plain = issue(client, 'Patch the outage', priority=2)
    pin(client, 'issues', pinned)
    moved = client.post(f'/issues/{plain}/rank', json={'before': pinned})
    assert moved.status_code == status.HTTP_409_CONFLICT, show_status_and_response(moved)
    assert f'#{pinned} is pinned' in moved.json()['message']

    ok(client.patch(f'/issues/{plain}', json={'status': 'completed'}))
    someday = initiative(client, 'Measure everything', status='someday')
    shelved = issue(client, 'Count the tokens', initiative=someday)
    for path in (f'/issues/{plain}/pin', f'/initiatives/{someday}/pin', f'/issues/{shelved}/pin'):
        refused = client.post(path, json={})
        assert refused.status_code == status.HTTP_409_CONFLICT, show_status_and_response(refused)
