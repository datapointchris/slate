from uuid import UUID

import pytest
from fastapi import status
from sqlalchemy import func
from sqlalchemy import select

from slate import models
from tests.conftest import TEST_PERSON
from tests.session_database import insert_test_data_transactional
from tests.util import show_status_and_response

IDEAS = '/ideas'
FOLLOW = {'Slate-Follow-Redirects': 'true'}


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


FOLDER = 'claude-code/fold'


def new_initiative(client, **fields) -> dict:
    body = {'name': 'Narrate the fleet from its tables', 'summary': 'One agent writes each narration.', **fields}
    return ok(client.post('/initiatives', json=body), status.HTTP_201_CREATED)['initiative']


def test_fold_moves_every_row_and_redirects_the_number(client, txn_api_logged_in):
    _, session = txn_api_logged_in
    for name in ('security', 'data'):
        ok(client.post('/tags', json={'name': name, 'description': f'Work on {name}'}), status.HTTP_201_CREATED)
    pursuits = [
        ok(client.post('/pursuits', json={'name': name, 'summary': 'It holds work'}), status.HTTP_201_CREATED)['pursuit']['number']
        for name in ('Data Warehouse', 'Personal OS')
    ]
    target = new_initiative(client, tags=['security'], pursuits=pursuits[:1], refs=['inbox:1'])['number']
    ok(client.post('/claims', json={'record': target, 'holder': FOLDER, 'step': 'design'}))
    ok(client.put(f'/documents/{target}/design', json={'kind': 'design', 'body': 'The target design\n'}), status.HTTP_201_CREATED)

    idea = new_idea(client, tags=['security', 'data'], pursuits=pursuits, refs=['inbox:1', 'inbox:2'])
    number = idea['number']
    ok(client.post('/claims', json={'record': number, 'holder': 'claude-code/shape', 'step': 'shape'}))
    ok(client.post(f'/claims/{number}/release', json={'released_by': 'claude-code/shape'}))
    ok(client.post('/claims', json={'record': number, 'holder': FOLDER}))
    ok(client.post(f'/claims/{number}/wait', json={'holder': FOLDER, 'waiting_on': 'a review'}))
    ok(client.post('/comments', json={'record': number, 'body': 'Worth a narration'}), status.HTTP_201_CREATED)
    ok(client.put(f'/documents/{number}/design', json={'kind': 'design', 'body': 'Line one\nLine two\n'}), status.HTTP_201_CREATED)
    line = {'record': number, 'document': 'design', 'line': 1, 'body': 'About line one'}
    ok(client.post('/comments', json=line), status.HTTP_201_CREATED)
    session.add(models.RecordCheck(record_number=number, checked_by='a-check', verdict='holds', evidence='It still holds'))
    session.flush()
    earlier = new_idea(client, title='Read the fleet each morning')['number']
    ok(client.post(f'{IDEAS}/{earlier}/fold', json={'into': number, 'holder': FOLDER}))
    # A released CLI reads an idea, then deletes it by the id that read answered, and asks for no redirect.
    unasked = client.get(f'{IDEAS}/{earlier}')
    assert (unasked.status_code, f'#{earlier} was folded into idea #{number}' in unasked.json()['message']) == (404, True)
    assert ok(client.get(f'{IDEAS}/{earlier}', headers=FOLLOW))['number'] == number

    folded = ok(client.post(f'{IDEAS}/{number}/fold', json={'into': target, 'holder': FOLDER}))
    assert folded['initiative']['number'] == target
    assert {effect['code'] for effect in folded['side_effects']} >= {
        'claim_ended',
        'worklog_moved',
        'claims_moved',
        'comments_moved',
        'checks_moved',
        'document_moved',
        'refs_moved',
        'tags_moved',
        'pursuits_moved',
        'redirect_retargeted',
        'record_redirected',
    }
    assert [ok(client.get(f'/worklogs/{number}.{sequence}'))['name'] for sequence in (1, 2)] == [f'{target}.2', f'{target}.3']
    claims = ok(client.get(f'/records/{target}'))['record']['claims']
    assert {(claim['holder'], claim['ending'], claim['ended_by']) for claim in claims} == {
        (FOLDER, None, None),
        (FOLDER, 'released', FOLDER),
        ('claude-code/shape', 'released', 'claude-code/shape'),
    }

    renamed = ok(client.get(f'/documents/{number}/design', headers=FOLLOW))
    assert (renamed['name'], [comment['body'] for comment in renamed['comments']]) == (f'design-from-{number}', ['About line one'])
    assert ok(client.get(f'/documents/{target}/design'))['name'] == 'design'

    read = ok(client.get(f'/records/{number}', headers=FOLLOW))
    assert (read['kind'], read['number']) == ('initiative', target)
    assert [(row['number'], row['sentence'][: row['sentence'].index(' on ')]) for row in read['record']['redirected_from']] == [
        (earlier, f'#{earlier} was folded into initiative #{target}'),
        (number, f'#{number} was folded into initiative #{target}'),
    ]
    assert (read['record']['tags'], sorted(row['ref'] for row in read['record']['refs'])) == (['data', 'security'], ['inbox:1', 'inbox:2'])
    assert sorted(pursuit['number'] for pursuit in read['record']['pursuits']) == sorted(pursuits)
    assert ok(client.get(f'/records/{earlier}', headers=FOLLOW))['number'] == target
    assert ok(client.get(f'/initiatives/{idea["id"]}', headers=FOLLOW))['number'] == target
    missing = client.get(f'{IDEAS}/{number}')
    assert (missing.status_code, f'initiative #{target}' in missing.json()['message']) == (status.HTTP_404_NOT_FOUND, True)
    assert number not in [row['number'] for row in ok(client.get('/queue', params={'step': 'discuss'}))['queue']]
    assert ('ideas', 'delete') in {(row['table_name'], row['operation']) for row in ok(client.get(f'/records/{target}/changes'))}

    completed = new_initiative(client, name='Shipped narration', status='completed')['number']
    assert ok(client.post(f'{IDEAS}/{new_idea(client)["number"]}/fold', json={'into': completed}))['initiative']['status'] == 'completed'

    deleted = ok(client.delete(f'/initiatives/{target}'))
    assert [effect['message'] for effect in deleted['side_effects'] if effect['code'] == 'redirect_deleted'] == [
        f'#{retired} led to #{target}, and answers 404 now.' for retired in sorted((earlier, number))
    ]


def test_fold_refuses_what_it_cannot_move(client, txn_api_logged_in):
    _, session = txn_api_logged_in
    idea = new_idea(client)['number']
    target = new_initiative(client)['number']
    pursuit = ok(client.post('/pursuits', json={'name': 'Data Warehouse', 'summary': 'It holds work'}), status.HTTP_201_CREATED)['pursuit']

    def refused(response, names: str) -> None:
        assert (response.status_code, names in response.json()['message']) == (status.HTTP_409_CONFLICT, True), show_status_and_response(
            response
        )

    refused(client.post(f'{IDEAS}/{target}/fold', json={'into': idea}), 'is an initiative')
    refused(client.post(f'{IDEAS}/{idea}/fold', json={'into': pursuit['number']}), 'is a pursuit')
    refused(client.post(f'{IDEAS}/{idea}/fold', json={'into': idea}), 'itself')
    # `slate ideas fold <idea> --into <initiative>` on a pair promoted before the number was kept,
    # whose initiative names the idea in `idea_id`.
    named = new_idea(client)
    pair = new_initiative(client, name='Made before the number was kept')['number']
    session.scalars(select(models.Initiative).where(models.Initiative.number == pair)).one().idea_id = UUID(named['id'])
    session.flush()
    refused(client.post(f'{IDEAS}/{named["number"]}/fold', json={'into': pair}), 'names it as where that work came from')
    for held in (idea, target):
        ok(client.post('/claims', json={'record': held, 'holder': 'claude-code/other'}))
        refused(client.post(f'{IDEAS}/{idea}/fold', json={'into': target, 'holder': FOLDER}), 'claude-code/other holds')
        ok(client.post(f'/claims/{held}/release', json={'released_by': 'claude-code/other'}))
    assert (session.get(models.Record, idea).kind, ok(client.get(f'/initiatives/{target}'))['redirected_from']) == ('idea', [])

    ok(client.post(f'{IDEAS}/{idea}/fold', json={'into': target}))
    refused(client.delete(f'{IDEAS}/{idea}'), f'run it against #{target}')
    refused(client.patch(f'{IDEAS}/{idea}', json={'title': 'Renamed'}), f'run it against #{target}')
    refused(client.post('/comments', json={'record': idea, 'body': 'Late word'}), f'run it against #{target}')
    shown = ok(client.get(f'/initiatives/{target}'))
    assert (shown['name'], shown['comments']) == ('Narrate the fleet from its tables', [])
