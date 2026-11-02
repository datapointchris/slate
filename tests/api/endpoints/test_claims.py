import datetime as dt

import pytest
from fastapi import status
from sqlalchemy import update

from slate import models
from tests.session_database import insert_test_data_transactional
from tests.util import show_status_and_response

CLAIMS = '/claims'
ISSUES = '/issues'
INITIATIVES = '/issues/initiatives'


@pytest.fixture
def seeded(txn_api_logged_in):
    client, session = txn_api_logged_in
    insert_test_data_transactional(session, 'issues', 'issue_labels', 'issue_initiatives')
    return client, session


@pytest.fixture
def client(seeded):
    return seeded[0]


def ok(response, code=status.HTTP_200_OK):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()


def number(client, title: str) -> int:
    return next(row['number'] for row in ok(client.get(ISSUES, params={'status': 'all'})) if row['title'] == title)


def initiative_number(client, name: str) -> int:
    return ok(client.get(f'{INITIATIVES}/{name}'))['number']


def hold(client, record: int, holder: str, **fields) -> dict:
    return ok(client.post(CLAIMS, json={'record': record, 'holder': holder, **fields}))


def codes(effects: list[dict]) -> list[tuple[str, int | None]]:
    return [(effect['code'], effect['record']) for effect in effects]


def ready_titles(client) -> list[str]:
    return [row['title'] for row in ok(client.get(f'{ISSUES}/ready'))]


def test_holding_the_queue_takes_each_issue_once_then_none(client):
    held = [ok(client.post(f'{CLAIMS}/next', json={'holder': f'agent-{n}'})) for n in range(4)]
    assert [answer['claim']['record']['title'] if answer['claim'] else None for answer in held] == [
        'Ready bug with high priority',
        'Ready chore with low priority',
        'Ready task without priority',
        None,
    ]
    assert [warning['code'] for warning in held[-1]['warnings']] == ['nothing_ready']


def test_another_holders_live_claim_refuses_the_hold_by_name(client):
    bug = number(client, 'Ready bug with high priority')
    hold(client, bug, 'agent-a')
    refused = client.post(CLAIMS, json={'record': bug, 'holder': 'agent-b'})
    assert refused.status_code == status.HTTP_409_CONFLICT, show_status_and_response(refused)
    assert 'agent-a' in refused.json()['message']


def test_holding_again_changes_nothing_and_only_an_extend_moves_the_expiry(client):
    bug = number(client, 'Ready bug with high priority')
    first = hold(client, bug, 'agent-a', minutes=10)['claim']
    again = hold(client, bug, 'agent-a', minutes=600)
    assert (again['claim']['id'], again['claim']['expires_ts']) == (first['id'], first['expires_ts'])
    assert [warning['code'] for warning in again['warnings']] == ['already_held']

    extended = ok(client.post(f'{CLAIMS}/{bug}/extend', json={'holder': 'agent-a', 'minutes': 600}))['claim']
    assert dt.datetime.fromisoformat(extended['expires_ts']) > dt.datetime.fromisoformat(first['expires_ts'])


def test_an_expired_claim_frees_the_issue_and_ends_as_expired_once_taken_over(seeded):
    client, session = seeded
    bug = number(client, 'Ready bug with high priority')
    gone = hold(client, bug, 'agent-gone')['claim']
    assert 'Ready bug with high priority' not in ready_titles(client)

    lapsed = dt.datetime.now(dt.UTC) - dt.timedelta(minutes=1)
    session.execute(
        update(models.Claim).where(models.Claim.id == gone['id']).values(started_ts=lapsed - dt.timedelta(hours=1), expires_ts=lapsed)
    )
    session.flush()
    assert ready_titles(client)[0] == 'Ready bug with high priority'

    taken = hold(client, bug, 'agent-next')
    assert codes(taken['side_effects']) == [('expired_claim_ended', bug)]
    first, second = ok(client.get(f'{ISSUES}/{bug}'))['claims']
    assert (first['holder'], first['ending'], first['state'], first['ended_ts']) == ('agent-gone', 'expired', 'ended', first['expires_ts'])
    assert (second['holder'], second['state']) == ('agent-next', 'live')


def test_an_issue_claimed_released_and_claimed_again_shows_both_claims(client):
    bug = number(client, 'Ready bug with high priority')
    hold(client, bug, 'agent-a')
    ok(client.post(f'{CLAIMS}/{bug}/release', json={'released_by': 'agent-a'}))
    hold(client, bug, 'agent-b')

    claims = ok(client.get(f'{ISSUES}/{bug}'))['claims']
    assert [(claim['holder'], claim['ending'], claim['state']) for claim in claims] == [
        ('agent-a', 'released', 'ended'),
        ('agent-b', None, 'live'),
    ]
    assert claims[0]['started_ts'] <= claims[0]['ended_ts'] <= claims[1]['started_ts']
    assert claims[1]['ended_ts'] is None


def test_holding_an_issue_in_a_claimed_initiative_goes_through_and_names_its_holder(client):
    task = number(client, 'Ready task without priority')
    ok(client.patch(f'{ISSUES}/{task}', json={'initiative': 'Ship the issue tracker'}))
    initiative = initiative_number(client, 'Ship the issue tracker')
    hold(client, initiative, 'claude-code/lead')

    answer = hold(client, task, 'claude-code/worker')
    assert answer['claim']['holder'] == 'claude-code/worker'
    (warning,) = [warning for warning in answer['warnings'] if warning['code'] == 'claimed_initiative']
    assert warning['record'] == initiative
    assert 'claude-code/lead' in warning['message']


def test_holding_an_initiative_names_each_issue_in_it_another_holds(client):
    task = number(client, 'Ready task without priority')
    ok(client.patch(f'{ISSUES}/{task}', json={'initiative': 'Ship the issue tracker'}))
    hold(client, task, 'claude-code/worker')

    answer = hold(client, initiative_number(client, 'Ship the issue tracker'), 'claude-code/lead')
    assert codes(answer['warnings']) == [('claimed_issue_in_initiative', task)]
    assert 'claude-code/worker' in answer['warnings'][0]['message']


def test_a_hold_warns_about_each_open_dependency_and_each_claimed_dependent(client):
    bug = number(client, 'Ready bug with high priority')
    chore = number(client, 'Ready chore with low priority')
    task = number(client, 'Ready task without priority')
    ok(client.post(f'{ISSUES}/{bug}/dependencies', json={'depends_on': chore}), status.HTTP_201_CREATED)
    ok(client.post(f'{ISSUES}/{task}/dependencies', json={'depends_on': bug}), status.HTTP_201_CREATED)
    hold(client, task, 'agent-dependent')

    assert codes(hold(client, bug, 'agent-a')['warnings']) == [('open_dependency', chore), ('claimed_dependent', task)]


@pytest.mark.parametrize(('title', 'code'), [('Filed by a hook into triage', 'in_triage'), ('Completed feature', 'record_closed')])
def test_a_triage_or_closed_issue_is_held_with_a_warning(client, title, code):
    answer = hold(client, number(client, title), 'agent')
    assert answer['claim']['state'] == 'live'
    assert [warning['code'] for warning in answer['warnings']] == [code]


def test_a_person_may_release_another_holders_claim_and_is_warned(client):
    bug = number(client, 'Ready bug with high priority')
    hold(client, bug, 'agent-a')
    answer = ok(client.post(f'{CLAIMS}/{bug}/release', json={'released_by': 'chris@desk'}))
    assert (answer['claim']['ending'], answer['claim']['ended_by']) == ('released', 'chris@desk')
    assert [warning['code'] for warning in answer['warnings']] == ['released_anothers_claim']

    nothing = ok(client.post(f'{CLAIMS}/{bug}/release', json={'released_by': 'chris@desk'}))
    assert (nothing['claim'], [warning['code'] for warning in nothing['warnings']]) == (None, ['nothing_to_release'])


def test_completing_an_initiative_ends_its_claim_naming_it(client):
    initiative = initiative_number(client, 'Retire the old capture path')
    hold(client, initiative, 'claude-code/lead')
    answer = ok(client.patch(f'{INITIATIVES}/{initiative}', json={'status': 'completed'}))
    assert codes(answer['side_effects']) == [('claim_ended', initiative)]
    assert answer['initiative']['claim'] is None


def test_the_list_reads_who_holds_what_by_holder_prefix_and_state(client):
    bug = number(client, 'Ready bug with high priority')
    chore = number(client, 'Ready chore with low priority')
    hold(client, bug, 'claude-code/a1b2')
    hold(client, chore, 'worker/fix')
    ok(client.post(f'{CLAIMS}/{chore}/release', json={'released_by': 'worker/fix'}))

    live = ok(client.get(CLAIMS, params={'holder': 'claude-code/'}))
    assert [(claim['record']['number'], claim['record']['kind'], claim['state']) for claim in live] == [(bug, 'issue', 'live')]
    assert [claim['holder'] for claim in ok(client.get(CLAIMS, params={'state': 'ended'}))] == ['worker/fix']
