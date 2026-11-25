"""`GET /queue`: every kind of open work by priority, each row's step, and held work apart."""

import pytest
from fastapi import status
from sqlalchemy import event

from slate.services import queue_board
from slate.services import work_queue
from slate.services.issue_readiness import measure_readiness
from slate.services.issue_views import calendar_now
from tests.util import check_step
from tests.util import show_status_and_response

QUEUE = '/queue'
BOARD = '/queue/board'


@pytest.fixture
def client(txn_api_logged_in):
    return txn_api_logged_in[0]


def ok(response, code=status.HTTP_200_OK):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()


def issue(client, title: str, **fields) -> int:
    return ok(client.post('/issues', json={'title': title, **fields}), status.HTTP_201_CREATED)['issue']['number']


def initiative(client, name: str, **fields) -> int:
    return ok(client.post('/initiatives', json={'summary': 'It is done.', 'name': name, **fields}), status.HTTP_201_CREATED)['initiative'][
        'number'
    ]


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
        ('Plan the importer', 'shape', 1),
        ('Schedule review rounds', 'discuss', 2),
        ('Pick a storage format', 'decide', 3),
        ('Write the parser', 'build', 4),
    ]
    assert [(row['title'], row['holder']) for row in ok(client.get(QUEUE))['claimed']] == [('Fix the flaky test', 'agent-a')]


def test_each_initiatives_issues_run_together_and_move_with_it(client):
    """Filed interleaved, each initiative's issues still run together, in the order the initiatives rank."""
    importer = initiative(client, 'Ship the importer', priority=1)
    exporter = initiative(client, 'Ship the exporter', priority=1)
    for planned in (importer, exporter):
        check_step(client, planned, 'preflight')
    for step in (1, 2):
        issue(client, f'Import stage {step}', initiative=importer)
        issue(client, f'Export stage {step}', initiative=exporter)
    issue(client, 'Patch the outage', priority=1)

    importer_first = ['Import stage 1', 'Import stage 2', 'Export stage 1', 'Export stage 2', 'Patch the outage']
    assert [title for title, _, _ in rows(client)] == importer_first

    ok(client.post(f'/initiatives/{exporter}/rank', json={'before': importer}))
    exporter_first = ['Export stage 1', 'Export stage 2', 'Import stage 1', 'Import stage 2', 'Patch the outage']
    assert [title for title, _, _ in rows(client)] == exporter_first


def test_an_initiative_lists_at_the_step_after_its_furthest_checked_one_beside_its_issues_at_build(client):
    shaped = initiative(client, 'Ship the importer', priority=1)
    designed = initiative(client, 'Ship the exporter', priority=2)
    preflighted = initiative(client, 'Ship the syncer', priority=3)
    unstepped = initiative(client, 'Ship the parser', priority=4)
    issue(client, 'Write the exporter', initiative=designed)
    issue(client, 'Write the parser', initiative=unstepped)
    check_step(client, shaped, 'shape')
    for step in ('shape', 'design'):
        check_step(client, designed, step)
    check_step(client, preflighted, 'preflight')

    assert rows(client) == [
        ('Ship the importer', 'design', 1),
        ('Ship the exporter', 'preflight', 2),
        ('Write the exporter', 'build', 2),
        ('Ship the syncer', 'retro', 3),
        ('Ship the parser', 'shape', 4),
        ('Write the parser', 'build', 4),
    ]
    assert [row['title'] for row in ok(client.get(QUEUE, params={'step': 'design'}))['queue']] == ['Ship the importer']


def test_a_decision_the_work_waits_on_is_decided_at_that_works_priority(client):
    decision = issue(client, 'Choose the wire format', type='decision')
    issue(client, 'Ship the sync', priority=2, depends_on=[decision])
    assert rows(client) == [('Choose the wire format', 'decide', 2)]


def test_a_records_work_holds_everything_it_transitively_waits_on_with_what_each_unblocks(client):
    """A coordinator taking this plan to done reads it whole, across initiatives, ready or not."""
    plan = initiative(client, 'Ship the importer', priority=2)
    other = initiative(client, 'Ship the exporter', priority=3)
    for planned in (plan, other):
        check_step(client, planned, 'preflight')
    first = issue(client, 'Import stage 1', initiative=plan)
    second = issue(client, 'Import stage 2', initiative=plan)
    outside = issue(client, 'Shared parser', initiative=other)
    deeper = issue(client, 'Parser grammar', priority=4)
    child = issue(client, 'Grammar test corpus', parent=deeper)
    finished = issue(client, 'Finished work')
    unrelated = issue(client, 'Patch the outage', priority=1)
    for waiter, waited_on in ((first, outside), (outside, deeper), (second, first), (second, finished)):
        ok(client.post(f'/issues/{waiter}/dependencies', json={'depends_on': waited_on}), status.HTTP_201_CREATED)
    ok(client.patch(f'/issues/{finished}', json={'status': 'completed'}))

    read = ok(client.get(QUEUE, params={'for_record': plan}))['queue']
    unblocks = {row['number']: row['unblocks'] for row in read}

    assert unblocks == {first: [second], second: [], outside: [first], deeper: [outside], child: [deeper]}
    assert [row['number'] for row in read] == [child, deeper, outside, first, second]
    assert unrelated not in unblocks and finished not in unblocks

    assert {row['number'] for row in ok(client.get(QUEUE, params={'for_record': outside}))['queue']} == {outside, deeper, child}
    assert ok(client.get(QUEUE, params={'for_record': 999999}), status.HTTP_404_NOT_FOUND)


def test_a_claim_on_an_initiative_takes_its_whole_plan_out_of_the_queue_for_everyone_but_its_holder(client):
    plan = initiative(client, 'Ship the importer', priority=2)
    blocked = issue(client, 'Write the importer', initiative=plan)
    ready = issue(client, 'Document the importer', initiative=plan)
    question = issue(client, 'Pick the format', initiative=plan, type='decision')
    dependency = issue(client, 'Shared parser', priority=4)
    free = issue(client, 'Patch the outage', priority=4)
    ok(client.post(f'/issues/{blocked}/dependencies', json={'depends_on': dependency}), status.HTTP_201_CREATED)
    hold(client, plan, 'coordinator')

    read = ok(client.get(QUEUE))
    assert {row['number'] for row in read['queue']} == {question, free}
    plan_rows = {row['number']: (row['holder'], row['held_through']) for row in read['claimed'] if row['kind'] == 'issue'}
    assert plan_rows == {ready: ('coordinator', plan), dependency: ('coordinator', plan)}

    assert {ready, dependency, blocked} <= {row['number'] for row in ok(client.get(QUEUE, params={'for_record': plan}))['queue']}
    (progress,) = ok(client.get('/initiatives/progress', params={'initiative': plan}))
    assert ready in {row['number'] for row in progress['next']}

    assert ok(client.post('/claims/next', json={'holder': 'someone-else'}))['claim']['record_number'] == free
    assert ok(client.post('/claims/next', json={'holder': 'coordinator'}))['claim']['record_number'] in {ready, dependency}


def test_the_board_lists_the_queues_rows_in_its_order_in_the_same_queries_however_many(client, txn_api_logged_in):
    """A poll every 15 seconds pays each query four times a minute, so a query per row is the failure this pins."""
    _, session = txn_api_logged_in
    created = ok(client.post('/pursuits', json={'name': 'Interaction', 'summary': 'It holds work'}), status.HTTP_201_CREATED)
    pursuit = {'number': created['pursuit']['number'], 'name': 'Interaction'}

    def filed(prefix: str) -> dict[str, int]:
        coordinator, repo = f'{prefix.lower()}-coordinator', f'{prefix.lower()}-app'
        ok(client.post('/ideas', json={'title': f'{prefix} idea'}), status.HTTP_201_CREATED)
        plan = initiative(client, f'{prefix} plan', priority=2, pursuits=[pursuit['number']])
        built = issue(client, f'{prefix} build', initiative=plan, repo=repo, paths=['src'])
        held_plan = initiative(client, f'{prefix} held plan', priority=3)
        check_step(client, held_plan, 'preflight')
        planned = issue(client, f'{prefix} planned', initiative=held_plan)
        hold(client, held_plan, coordinator)
        ok(client.post(f'/claims/{held_plan}/wait', json={'holder': coordinator, 'waiting_on': 'a read of its plan'}))
        hold(client, issue(client, f'{prefix} held', priority=1, repo=repo, paths=['src/api']), f'{prefix.lower()}-worker')
        issue(client, f'{prefix} decision', type='decision', priority=4)
        return {'plan': plan, 'built': built, 'held_plan': held_plan, 'planned': planned}

    def queries_to_read() -> int:
        statements: list[str] = []

        def asked(_conn, _cursor, statement, *_rest):
            statements.append(statement)

        engine = session.get_bind()
        event.listen(engine, 'before_cursor_execute', asked)
        try:
            ok(client.get(BOARD))
        finally:
            event.remove(engine, 'before_cursor_execute', asked)
        return len(statements)

    first = filed('First')
    asked = queries_to_read()
    filed('More')
    filed('Most')
    assert queries_to_read() == asked

    board, queue = ok(client.get(BOARD)), ok(client.get(QUEUE))
    for part in ('queue', 'claimed'):
        assert [row['number'] for row in board[part]] == [row['number'] for row in queue[part]]

    rows = {row['number']: row for row in board['queue'] + board['claimed']}
    plan, built, held_plan, planned = (rows[first[name]] for name in ('plan', 'built', 'held_plan', 'planned'))
    assert (plan['kind'], plan['step'], plan['open_count'], plan['repos'], plan['pursuits']) == (
        'initiative',
        'shape',
        1,
        ['first-app'],
        [pursuit],
    )
    assert ((built['initiative']['number'], built['initiative']['name']), built['repos'], built['pursuits'], built['open_count']) == (
        (first['plan'], 'First plan'),
        ['first-app'],
        [pursuit],
        None,
    )
    assert [overlap['holder'] for overlap in built['overlaps']] == ['first-worker']
    assert (held_plan['holder'], held_plan['held_through'], held_plan['waiting']['waiting_on']) == (
        'first-coordinator',
        None,
        'a read of its plan',
    )
    assert (planned['holder'], planned['held_through'], planned['waiting'], planned['expires_ts']) == (
        'first-coordinator',
        first['held_plan'],
        held_plan['waiting'],
        held_plan['expires_ts'],
    )


def test_each_row_names_what_placed_it_one_hop_back(client):
    def depends(waiter: int, waited_on: int) -> None:
        ok(client.post(f'/issues/{waiter}/dependencies', json={'depends_on': waited_on}), status.HTTP_201_CREATED)

    alone = issue(client, 'Patch the outage', priority=3)
    pinned = issue(client, 'Water the plants', priority=4)
    pin(client, 'issues', pinned)
    plan = initiative(client, 'Ship the importer', priority=3)
    check_step(client, plan, 'preflight')
    inside = issue(client, 'Import stage 1', initiative=plan)
    parent = issue(client, 'Split the export', priority=2)
    child = issue(client, 'Export the rows', parent=parent)
    gated = issue(client, 'Ship the sync', priority=2)
    blocker = issue(client, 'Fix the build', priority=4)
    depends(gated, blocker)
    pinned_plan = initiative(client, 'Ship the exporter', priority=4)
    check_step(client, pinned_plan, 'preflight')
    pin(client, 'initiatives', pinned_plan)
    pulled = issue(client, 'Export stage 1', initiative=pinned_plan)
    kept = issue(client, 'Export stage 2', initiative=pinned_plan)
    urgent = issue(client, 'Ship the export today', priority=1)
    pin(client, 'issues', urgent)
    depends(urgent, pulled)

    placed = {
        alone: ('own', None, None, alone),
        pinned: ('pin', None, None, pinned),
        inside: ('initiative', None, plan, plan),
        child: ('parent', parent, None, parent),
        blocker: ('waiter', gated, None, gated),
        pulled: ('waiter', urgent, pinned_plan, urgent),
        kept: ('initiative', None, pinned_plan, pinned_plan),
    }
    board = ok(client.get(BOARD))['queue']
    assert {
        row['number']: (row['placed_by'], row['raised_by'], (row['initiative'] or {}).get('number'), row['slot']) for row in board
    } == placed
    assert {row['number']: row['placed_by'] for row in ok(client.get(QUEUE))['queue']} == {
        number: placed_by for number, (placed_by, _, _, _) in placed.items()
    }


def test_each_board_row_says_where_the_rank_write_moves_it_and_the_write_lands_it_there(client):
    """A waiter's two blockers each wait on a ready issue, so those two draw side by side and move past each other."""

    def depends(waiter: int, waited_on: int) -> None:
        ok(client.post(f'/issues/{waiter}/dependencies', json={'depends_on': waited_on}), status.HTTP_201_CREATED)

    def idea(title: str, priority: int) -> int:
        return ok(client.post('/ideas', json={'title': title, 'priority': priority}), status.HTTP_201_CREATED)['idea']['number']

    pinned = idea('Water the plants', 2)
    pin(client, 'ideas', pinned)
    urgent = issue(client, 'Ship the export today', priority=1)
    first_wait, second_wait = issue(client, 'Export the rows'), issue(client, 'Export the columns')
    first_raised, second_raised = issue(client, 'Read the rows'), issue(client, 'Read the columns')
    depends(urgent, first_wait)
    depends(urgent, second_wait)
    depends(first_wait, first_raised)
    depends(second_wait, second_raised)
    urgent_idea = idea('Try a layout', 1)
    plan = initiative(client, 'Ship the importer', priority=2)
    check_step(client, plan, 'preflight')
    first_inside, second_inside = issue(client, 'Import stage 1', initiative=plan), issue(client, 'Import stage 2', initiative=plan)
    last = idea('Schedule review rounds', 2)

    def board() -> list[dict]:
        return ok(client.get(BOARD))['queue']

    def answered(move: dict) -> object:
        assert (move['beside'] is None) == (move['refused'] is not None), move
        return 'refused' if move['refused'] else (move['beside'], move['passes'])

    drawn = board()
    assert [row['number'] for row in drawn] == [pinned, first_raised, second_raised, urgent_idea, first_inside, second_inside, last]
    assert {row['number']: (answered(row['moves']['ahead']), answered(row['moves']['behind'])) for row in drawn} == {
        pinned: ('refused', 'refused'),
        first_raised: ('refused', (second_raised, [second_raised])),
        second_raised: ((first_raised, [first_raised]), 'refused'),
        urgent_idea: ((urgent, [second_raised, first_raised]), 'refused'),
        first_inside: ('refused', (second_inside, [second_inside])),
        second_inside: ((first_inside, [first_inside]), 'refused'),
        last: ((plan, [second_inside, first_inside]), 'refused'),
    }
    refused = client.post(f'/issues/{second_raised}/rank', json={'after': first_inside})
    assert refused.json()['message'] == drawn[2]['moves']['behind']['refused']

    ok(client.post(f'/issues/{second_raised}/rank', json={'before': first_raised}))
    ok(client.post(f'/ideas/{urgent_idea}/rank', json={'before': urgent}))
    ok(client.post(f'/ideas/{last}/rank', json={'before': plan}))
    assert [row['number'] for row in board()] == [pinned, urgent_idea, second_raised, first_raised, last, first_inside, second_inside]


def test_an_issue_filed_and_claimed_after_readiness_was_measured_lists_under_its_holder_alone(client, txn_api_logged_in, monkeypatch):
    """Each statement after the readiness pass sees what committed since, so a claim landing between them reaches the read unmeasured."""
    _, session = txn_api_logged_in
    today, now = calendar_now('UTC')
    measured_before = measure_readiness(session, today, now)
    late = issue(client, 'Filed mid-read', priority=2)
    hold(client, late, 'agent-a')
    for module in (work_queue, queue_board):
        monkeypatch.setattr(module, 'measure_readiness', lambda *_: measured_before)

    for read in (QUEUE, BOARD):
        (row,) = ok(client.get(read))['claimed']
        assert (row['number'], row['holder'], row['priority'], row['placed_by']) == (late, 'agent-a', 2, 'own')


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
    check_step(client, importer, 'preflight')
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
    promoted = ok(client.post(f'/ideas/{idea}/promote', json={'summary': 'It is done.', 'status': 'someday'}))
    assert promoted['initiative']['pinned_ts'] is None
    assert [effect['code'] for effect in promoted['side_effects']] == ['pin_cleared']


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


def test_an_initiative_skips_each_step_it_does_not_plan_and_once_none_is_left_lists_only_under_its_holder(client):
    """A coordinator holding an initiative that plans no retro still sees it in `claimed` once its last issue closes."""
    initiative(client, 'Ship the importer', priority=1, not_planned_steps=['shape'])
    lean = initiative(client, 'Ship the exporter', priority=2, not_planned_steps=['design'])
    finished = initiative(client, 'Ship the syncer', priority=3, not_planned_steps=['retro'])
    check_step(client, lean, 'shape')
    check_step(client, finished, 'preflight')
    issue(client, 'Write the exporter', initiative=lean)
    last = issue(client, 'Write the syncer', initiative=finished)
    hold(client, finished, 'coordinator')
    ok(client.patch(f'/issues/{last}', json={'status': 'completed'}))

    assert rows(client) == [('Ship the importer', 'design', 1), ('Ship the exporter', 'preflight', 2), ('Write the exporter', 'build', 2)]
    claimed = ok(client.get(QUEUE))['claimed']
    assert [(row['title'], row['step'], row['holder']) for row in claimed] == [('Ship the syncer', 'build', 'coordinator')]
