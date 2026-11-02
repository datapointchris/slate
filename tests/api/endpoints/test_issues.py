import datetime as dt
from zoneinfo import ZoneInfo

import pytest
from fastapi import status
from sqlalchemy import update

from slate import models
from slate import schemas
from tests.conftest import TEST_PERSON
from tests.session_database import insert_test_data_transactional
from tests.util import show_status_and_response

from .crud_test import ApiCrudTester

ISSUES = '/issues'

NEW_ISSUE = schemas.IssueCreate(title='Write the issues CLI', repo='webapp', type='feature', priority=3)


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


def refused(response, code):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()['message']


def titles(rows) -> list[str]:
    return [row['title'] for row in rows]


def issue(client, title: str) -> dict:
    return next(row for row in ok(client.get(ISSUES, params={'status': 'all'})) if row['title'] == title)


def create(client, **fields) -> dict:
    return ok(client.post(ISSUES, json={'title': 'Created in a test', **fields}), status.HTTP_201_CREATED)


def patch(client, ref, **fields):
    return client.patch(f'{ISSUES}/{ref}', json=fields)


@pytest.fixture
def crud(seeded):
    client, _ = seeded
    return client, ApiCrudTester(endpoint=ISSUES, new_obj=NEW_ISSUE, verify_attr='title', expected_length=6, list_params={'status': 'all'})


def test_read_one(crud):
    client, tester = crud
    tester.test_read_one(client)


def test_lifecycle(crud):
    client, tester = crud
    tester.test_lifecycle(client)


class TestReferences:
    def test_an_issue_answers_to_its_number(self, client):
        bug = issue(client, 'Ready bug with high priority')
        assert ok(client.get(f'{ISSUES}/{bug["number"]}'))['id'] == bug['id']

    def test_a_word_is_neither_a_uuid_nor_a_number(self, client):
        refused(client.get(f'{ISSUES}/tomorrow'), status.HTTP_422_UNPROCESSABLE_CONTENT)


class TestCreate:
    def test_a_new_issue_ranks_last(self, client):
        created = create(client)
        assert created['rank'] > max(row['rank'] for row in ok(client.get(ISSUES, params={'status': 'all'})) if row['id'] != created['id'])

    def test_a_new_issue_starts_open_or_in_triage(self, client):
        assert create(client, status='triage')['status'] == 'triage'
        detail = refused(client.post(ISSUES, json={'title': 'Done already', 'status': 'completed'}), 422)
        assert 'triage or open' in detail

    def test_an_unknown_type_names_the_known_ones(self, client):
        detail = refused(client.post(ISSUES, json={'title': 'x', 'type': 'epic'}), 422)
        assert 'decision' in detail

    def test_an_unknown_label_is_refused_rather_than_created(self, client):
        detail = refused(client.post(ISSUES, json={'title': 'x', 'labels': ['area-db']}), 422)
        assert 'area-db' in detail
        assert 'area-api' in detail

    def test_two_labels_from_one_group_are_refused(self, client):
        detail = refused(client.post(ISSUES, json={'title': 'x', 'labels': ['area-api', 'area-cli']}), 422)
        assert 'area' in detail

    def test_labels_from_different_groups_are_kept(self, client):
        assert create(client, labels=['needs-design', 'area-api'])['labels'] == ['area-api', 'needs-design']

    def test_a_dependency_given_at_create_blocks_the_new_issue(self, client):
        bug = issue(client, 'Ready bug with high priority')
        created = create(client, depends_on=[bug['number']])
        assert created['is_blocked'] is True
        assert created['is_ready'] is False
        assert [row['number'] for row in issue(client, bug['title'])['blocks']] == [created['number']]

    def test_depending_on_its_own_parent_is_refused(self, client):
        """The parent waits on its open child and the child on the parent, so neither would ever enter the ready queue."""
        parent = issue(client, 'Ready task without priority')
        detail = refused(client.post(ISSUES, json={'title': 'x', 'parent': parent['number'], 'depends_on': [parent['number']]}), 409)
        assert f'#{parent["number"]}' in detail

    def test_a_finished_initiative_takes_no_new_work(self, client):
        detail = refused(client.post(ISSUES, json={'title': 'x', 'initiative': 'Finished migration'}), 409)
        assert 'completed' in detail


class TestList:
    def test_closed_issues_are_left_out_unless_asked_for(self, client):
        assert 'Completed feature' not in titles(ok(client.get(ISSUES)))
        assert 'Completed feature' in titles(ok(client.get(ISSUES, params={'status': 'all'})))
        assert titles(ok(client.get(ISSUES, params={'status': 'completed'}))) == ['Completed feature']

    def test_an_unknown_status_is_a_422(self, client):
        refused(client.get(ISSUES, params={'status': 'done'}), 422)

    def test_order_is_priority_then_rank_with_no_priority_last(self, client):
        assert titles(ok(client.get(ISSUES))) == [
            'Ready bug with high priority',
            'Decision waiting on a person',
            'Ready chore with low priority',
            'Ready task without priority',
            'Filed by a hook into triage',
        ]

    def test_an_empty_repo_asks_for_issues_on_no_repo(self, client):
        assert titles(ok(client.get(ISSUES, params={'repo': ''}))) == ['Ready chore with low priority']

    def test_the_label_filter_narrows(self, client):
        create(client, title='Labeled', labels=['area-cli'])
        assert titles(ok(client.get(ISSUES, params={'label': 'area-cli'}))) == ['Labeled']

    def test_search_reads_title_and_description(self, client):
        assert titles(ok(client.get(ISSUES, params={'search': 'log bug'}))) == ['Ready bug with high priority']

    def test_blocked_narrows_before_the_limit(self, client):
        """The bug heads the list, so a limit taken first would leave nothing blocked to return."""
        create(client, title='Waits on the bug', depends_on=[issue(client, 'Ready bug with high priority')['number']])
        assert titles(ok(client.get(ISSUES, params={'blocked': 'true', 'limit': 1}))) == ['Waits on the bug']
        assert 'Waits on the bug' not in titles(ok(client.get(ISSUES, params={'blocked': 'false'})))

    def test_a_window_narrows_on_the_close(self, client):
        params = {'status': 'all', 'since': '2026-08-31T00:00:00Z', 'until': '2026-09-03T00:00:00Z'}
        assert titles(ok(client.get(ISSUES, params=params))) == ['Completed feature']

    def test_closed_by_reads_what_one_kind_of_closer_finished(self, client):
        """`claude-code/` is the attended sessions, apart from the unattended ones and the signed-in user."""
        for title, closer in (
            ('Attended', {'closed_by': 'claude-code/a1b2'}),
            ('Unattended', {'closed_by': 'claude-code-unattended/c3d4'}),
            ('Browser', {}),
        ):
            ok(patch(client, create(client, title=title)['number'], status='completed', **closer))
        assert titles(ok(client.get(ISSUES, params={'status': 'completed', 'closed_by': 'claude-code/'}))) == ['Attended']


class TestReadyQueue:
    def test_the_queue_leaves_out_decisions_and_triage(self, client):
        assert titles(ok(client.get(f'{ISSUES}/ready'))) == [
            'Ready bug with high priority',
            'Ready chore with low priority',
            'Ready task without priority',
        ]

    def test_decisions_are_read_by_asking_for_them(self, client):
        assert titles(ok(client.get(f'{ISSUES}/ready', params={'type': 'decision'}))) == ['Decision waiting on a person']

    def test_a_blocker_inherits_the_priority_of_what_it_gates(self, client):
        bug = issue(client, 'Ready bug with high priority')
        chore = issue(client, 'Ready chore with low priority')
        ok(client.post(f'{ISSUES}/{bug["number"]}/dependencies', json={'depends_on': chore['number']}), status.HTTP_201_CREATED)

        queue = ok(client.get(f'{ISSUES}/ready'))
        assert titles(queue) == ['Ready chore with low priority', 'Ready task without priority']
        assert queue[0]['priority'] == 4
        assert queue[0]['effective_priority'] == 2

    def test_an_initiative_raises_its_issues_and_an_own_low_priority_does_not_sink_one(self, client):
        task = issue(client, 'Ready task without priority')
        updated = ok(patch(client, task['number'], initiative='Ship the issue tracker'))
        assert updated['effective_priority'] == 2
        assert updated['initiative']['name'] == 'Ship the issue tracker'

        chore = issue(client, 'Ready chore with low priority')
        updated = ok(patch(client, chore['number'], initiative='Ship the issue tracker'))
        assert (updated['priority'], updated['effective_priority']) == (4, 2)

    def test_a_someday_initiatives_issues_wait_until_it_is_reopened(self, client):
        task = issue(client, 'Ready task without priority')
        ok(patch(client, task['number'], initiative='Ship the issue tracker'))
        ok(client.patch(f'{ISSUES}/initiatives/Ship the issue tracker', json={'status': 'someday'}))
        assert task['title'] not in titles(ok(client.get(f'{ISSUES}/ready')))
        detail = refused(client.post(f'{ISSUES}/{task["number"]}/claim', json={'claimant': 'agent'}), 409)
        assert 'Ship the issue tracker' in detail

        ok(client.patch(f'{ISSUES}/initiatives/Ship the issue tracker', json={'status': 'active'}))
        assert task['title'] in titles(ok(client.get(f'{ISSUES}/ready')))

    def test_active_work_depending_on_a_someday_issue_brings_it_into_the_queue(self, client):
        task = issue(client, 'Ready task without priority')
        bug = issue(client, 'Ready bug with high priority')
        ok(patch(client, task['number'], initiative='Ship the issue tracker'))
        ok(client.patch(f'{ISSUES}/initiatives/Ship the issue tracker', json={'status': 'someday'}))
        ok(client.post(f'{ISSUES}/{bug["number"]}/dependencies', json={'depends_on': task['number']}), status.HTTP_201_CREATED)

        queue = ok(client.get(f'{ISSUES}/ready'))
        assert queue[0]['title'] == task['title']
        assert queue[0]['effective_priority'] == 2

        ok(patch(client, bug['number'], status='completed'))
        assert task['title'] not in titles(ok(client.get(f'{ISSUES}/ready')))

    def test_autofix_false_on_an_issue_its_parent_or_its_initiative_keeps_it_from_the_autofixable_queue(self, client):
        bug = issue(client, 'Ready bug with high priority')
        task = issue(client, 'Ready task without priority')
        ok(patch(client, bug['number'], autofix=False))
        ok(patch(client, task['number'], initiative='Ship the issue tracker'))
        ok(client.patch(f'{ISSUES}/initiatives/Ship the issue tracker', json={'autofix': False}))
        parent = create(client, title='Parent with autofix off', autofix=False)
        child = create(client, title='Child of that parent', parent=parent['number'])

        assert titles(ok(client.get(f'{ISSUES}/ready', params={'autofixable': True}))) == ['Ready chore with low priority']
        everything = {bug['title'], task['title'], child['title'], 'Ready chore with low priority'}
        assert set(titles(ok(client.get(f'{ISSUES}/ready')))) == everything
        assert issue(client, task['title'])['is_autofixable'] is False

    def test_a_child_without_priority_takes_its_parent(self, client):
        bug = issue(client, 'Ready bug with high priority')
        child = create(client, parent=bug['number'])
        assert child['effective_priority'] == 2
        assert issue(client, bug['title'])['is_ready'] is False

    def test_a_deferred_issue_waits_for_its_day_in_the_readers_zone(self, client):
        """UTC+14 and UTC-11 are 25 hours apart, so their calendars never share a day."""
        task = issue(client, 'Ready task without priority')
        ahead = dt.datetime.now(ZoneInfo('Pacific/Kiritimati')).date().isoformat()
        ok(patch(client, task['number'], deferred_until_date=ahead))
        assert task['title'] in titles(ok(client.get(f'{ISSUES}/ready', params={'timezone': 'Pacific/Kiritimati'})))
        assert task['title'] not in titles(ok(client.get(f'{ISSUES}/ready', params={'timezone': 'Pacific/Pago_Pago'})))


class TestClaim:
    def test_claiming_the_queue_takes_each_issue_once_then_none(self, client):
        taken = [ok(client.post(f'{ISSUES}/ready/claim', json={'claimant': f'agent-{n}'}))['issue'] for n in range(4)]
        assert [row['title'] if row else None for row in taken] == [
            'Ready bug with high priority',
            'Ready chore with low priority',
            'Ready task without priority',
            None,
        ]
        assert taken[0]['status'] == 'in_progress'
        assert taken[0]['claimed_by'] == 'agent-0'

    def test_another_claimant_is_refused_by_name(self, client):
        bug = issue(client, 'Ready bug with high priority')
        ok(client.post(f'{ISSUES}/{bug["number"]}/claim', json={'claimant': 'agent-a'}))
        detail = refused(client.post(f'{ISSUES}/{bug["number"]}/claim', json={'claimant': 'agent-b'}), 409)
        assert 'agent-a' in detail

    def test_the_same_claimant_extends_its_claim(self, client):
        bug = issue(client, 'Ready bug with high priority')
        first = ok(client.post(f'{ISSUES}/{bug["number"]}/claim', json={'claimant': 'agent-a', 'minutes': 10}))
        second = ok(client.post(f'{ISSUES}/{bug["number"]}/claim', json={'claimant': 'agent-a', 'minutes': 600}))
        assert dt.datetime.fromisoformat(second['claim_expires_ts']) > dt.datetime.fromisoformat(first['claim_expires_ts'])

    def test_an_expired_claim_returns_the_issue_to_the_queue(self, seeded):
        client, session = seeded
        bug = issue(client, 'Ready bug with high priority')
        ok(client.post(f'{ISSUES}/{bug["number"]}/claim', json={'claimant': 'agent-gone'}))
        assert bug['title'] not in titles(ok(client.get(f'{ISSUES}/ready')))

        session.execute(
            update(models.Issue)
            .where(models.Issue.number == bug['number'])
            .values(claim_expires_ts=dt.datetime.now(dt.UTC) - dt.timedelta(minutes=1))
        )
        session.flush()
        assert titles(ok(client.get(f'{ISSUES}/ready')))[0] == bug['title']
        assert ok(client.post(f'{ISSUES}/{bug["number"]}/claim', json={'claimant': 'agent-next'}))['claimed_by'] == 'agent-next'

    @pytest.mark.parametrize(
        ('title', 'reason'),
        [('Filed by a hook into triage', 'triage'), ('Completed feature', 'completed')],
    )
    def test_triage_and_closed_issues_cannot_be_claimed(self, client, title, reason):
        target = issue(client, title)
        assert reason in refused(client.post(f'{ISSUES}/{target["number"]}/claim', json={'claimant': 'agent'}), 409)

    def test_a_blocked_issue_names_its_blockers(self, client):
        bug = issue(client, 'Ready bug with high priority')
        blocked = create(client, depends_on=[bug['number']])
        detail = refused(client.post(f'{ISSUES}/{blocked["number"]}/claim', json={'claimant': 'agent'}), 409)
        assert f'#{bug["number"]}' in detail

    def test_a_parent_with_an_open_child_names_the_child(self, client):
        """`complete` refuses the parent while the child is open, so a claim on it could never finish."""
        bug = issue(client, 'Ready bug with high priority')
        child = create(client, parent=bug['number'])
        detail = refused(client.post(f'{ISSUES}/{bug["number"]}/claim', json={'claimant': 'agent'}), 409)
        assert f'#{child["number"]}' in detail

    def test_a_deferred_issue_is_refused_until_its_day(self, client):
        task = issue(client, 'Ready task without priority')
        day = (dt.datetime.now(dt.UTC) + dt.timedelta(days=30)).date().isoformat()
        ok(patch(client, task['number'], deferred_until_date=day))
        detail = refused(client.post(f'{ISSUES}/{task["number"]}/claim', json={'claimant': 'agent'}), 409)
        assert day in detail

    def test_release_puts_the_issue_back_in_the_queue(self, client):
        bug = issue(client, 'Ready bug with high priority')
        ok(client.post(f'{ISSUES}/{bug["number"]}/claim', json={'claimant': 'agent'}))
        released = ok(client.delete(f'{ISSUES}/{bug["number"]}/claim'))
        assert (released['status'], released['claimed_by'], released['is_ready']) == ('open', None, True)


class TestStatusTransitions:
    def test_completing_stamps_the_close_and_drops_the_claim(self, client):
        bug = issue(client, 'Ready bug with high priority')
        ok(client.post(f'{ISSUES}/{bug["number"]}/claim', json={'claimant': 'agent'}))
        done = ok(patch(client, bug['number'], status='completed'))
        assert done['closed_ts'] is not None
        assert done['claimed_by'] is None

    def test_a_close_records_the_actor_the_caller_names(self, client):
        bug = issue(client, 'Ready bug with high priority')
        done = ok(patch(client, bug['number'], status='completed', closed_by='claude-code/a1b2'))
        assert done['closed_by'] == 'claude-code/a1b2'

    def test_a_close_naming_no_actor_records_the_signed_in_person(self, client):
        """A close naming nobody records the subject of the token it arrives with."""
        bug = issue(client, 'Ready bug with high priority')
        done = ok(patch(client, bug['number'], status='completed'))
        assert done['closed_by'] == TEST_PERSON.subject

    def test_a_move_between_closed_statuses_keeps_who_closed_it(self, client):
        task = issue(client, 'Ready task without priority')
        ok(patch(client, task['number'], status='completed', closed_by='first-closer'))
        canceled = ok(patch(client, task['number'], status='canceled', status_reason='Not needed'))
        assert canceled['closed_by'] == 'first-closer'

    def test_closed_by_is_sent_only_with_a_close(self, client):
        task = issue(client, 'Ready task without priority')
        detail = refused(patch(client, task['number'], title='Renamed', closed_by='someone'), 422)
        assert 'closed_by' in detail

    def test_reopening_clears_the_close_the_reason_and_the_duplicate(self, client):
        bug = issue(client, 'Ready bug with high priority')
        task = issue(client, 'Ready task without priority')
        ok(patch(client, task['number'], status='canceled', status_reason='Same bug', duplicate_of=bug['number']))
        reopened = ok(patch(client, task['number'], status='open'))
        assert (reopened['closed_ts'], reopened['closed_by'], reopened['status_reason'], reopened['duplicate_of']) == (
            None,
            None,
            None,
            None,
        )

    def test_canceling_needs_a_reason_or_a_duplicate(self, client):
        task = issue(client, 'Ready task without priority')
        refused(patch(client, task['number'], status='canceled'), 422)
        bug = issue(client, 'Ready bug with high priority')
        canceled = ok(patch(client, task['number'], status='canceled', duplicate_of=bug['number']))
        assert canceled['duplicate_of']['number'] == bug['number']

    def test_a_reason_belongs_only_to_a_canceled_issue(self, client):
        task = issue(client, 'Ready task without priority')
        refused(patch(client, task['number'], status_reason='Because'), 422)

    def test_a_duplicate_link_belongs_only_to_a_canceled_issue(self, client):
        task = issue(client, 'Ready task without priority')
        bug = issue(client, 'Ready bug with high priority')
        refused(patch(client, task['number'], duplicate_of=bug['number']), 422)

    def test_a_parent_is_not_completed_while_a_child_is_open(self, client):
        bug = issue(client, 'Ready bug with high priority')
        child = create(client, parent=bug['number'])
        detail = refused(patch(client, bug['number'], status='completed'), 409)
        assert f'#{child["number"]}' in detail

    def test_reopening_a_child_reopens_each_completed_ancestor(self, client):
        top = issue(client, 'Ready bug with high priority')
        middle = create(client, title='Middle', parent=top['number'])
        bottom = create(client, title='Bottom', parent=middle['number'])
        for finished in (bottom, middle, top):
            ok(patch(client, finished['number'], status='completed'))

        ok(patch(client, bottom['number'], status='open'))

        for reopened in (middle, top):
            row = ok(client.get(f'{ISSUES}/{reopened["number"]}'))
            assert (row['status'], row['closed_ts'], row['closed_by']) == ('open', None, None)

    def test_a_reopen_that_moves_the_child_leaves_the_old_parent_completed(self, client):
        old = create(client, title='Old parent')
        new = create(client, title='New parent')
        child = create(client, title='Moving child', parent=old['number'])
        ok(patch(client, child['number'], status='completed'))
        ok(patch(client, old['number'], status='completed'))

        ok(patch(client, child['number'], status='open', parent=new['number']))

        assert ok(client.get(f'{ISSUES}/{old["number"]}'))['status'] == 'completed'


class TestEdges:
    def test_a_dependency_cycle_is_refused_with_its_path(self, client):
        bug = issue(client, 'Ready bug with high priority')
        task = issue(client, 'Ready task without priority')
        ok(client.post(f'{ISSUES}/{bug["number"]}/dependencies', json={'depends_on': task['number']}), status.HTTP_201_CREATED)
        detail = refused(client.post(f'{ISSUES}/{task["number"]}/dependencies', json={'depends_on': bug['number']}), 409)
        assert f'#{task["number"]} → #{bug["number"]} → #{task["number"]}' in detail

    def test_a_parent_cannot_become_its_own_childs_child(self, client):
        bug = issue(client, 'Ready bug with high priority')
        child = create(client, parent=bug['number'])
        refused(patch(client, bug['number'], parent=child['number']), 409)

    def test_a_closed_issue_takes_no_new_open_child(self, client):
        completed = issue(client, 'Completed feature')
        task = issue(client, 'Ready task without priority')
        assert 'completed' in refused(client.post(ISSUES, json={'title': 'x', 'parent': completed['number']}), 409)
        assert 'completed' in refused(patch(client, task['number'], parent=completed['number']), 409)

    def test_a_dependency_is_added_once(self, client):
        bug = issue(client, 'Ready bug with high priority')
        task = issue(client, 'Ready task without priority')
        edge = {'depends_on': task['number']}
        ok(client.post(f'{ISSUES}/{bug["number"]}/dependencies', json=edge), status.HTTP_201_CREATED)
        refused(client.post(f'{ISSUES}/{bug["number"]}/dependencies', json=edge), 409)

    def test_removing_a_dependency_unblocks(self, client):
        bug = issue(client, 'Ready bug with high priority')
        task = issue(client, 'Ready task without priority')
        ok(client.post(f'{ISSUES}/{bug["number"]}/dependencies', json={'depends_on': task['number']}), status.HTTP_201_CREATED)
        response = client.delete(f'{ISSUES}/{bug["number"]}/dependencies/{task["number"]}')
        assert response.status_code == status.HTTP_204_NO_CONTENT, show_status_and_response(response)
        assert issue(client, bug['title'])['is_blocked'] is False
        refused(client.delete(f'{ISSUES}/{bug["number"]}/dependencies/{task["number"]}'), 404)

    def test_closing_a_blocker_unblocks(self, client):
        bug = issue(client, 'Ready bug with high priority')
        task = issue(client, 'Ready task without priority')
        ok(client.post(f'{ISSUES}/{bug["number"]}/dependencies', json={'depends_on': task['number']}), status.HTTP_201_CREATED)
        ok(patch(client, task['number'], status='completed'))
        assert issue(client, bug['title'])['is_ready'] is True

    def test_relabeling_replaces_the_set(self, client):
        created = create(client, labels=['area-api'])
        assert ok(patch(client, created['number'], labels=['area-cli', 'needs-design']))['labels'] == ['area-cli', 'needs-design']
        assert ok(patch(client, created['number'], labels=[]))['labels'] == []


class TestRank:
    def test_moving_before_places_it_directly_ahead(self, client):
        bug = issue(client, 'Ready bug with high priority')
        later = create(client, title='Filed later at high', priority=2)
        ok(client.post(f'{ISSUES}/{later["number"]}/rank', json={'before': bug['number']}))
        assert titles(ok(client.get(ISSUES)))[:2] == ['Filed later at high', 'Ready bug with high priority']

    def test_moving_after_lands_between_the_neighbors(self, client):
        bug = issue(client, 'Ready bug with high priority')
        create(client, title='Middle', priority=2)
        last = create(client, title='Last', priority=2)
        ok(client.post(f'{ISSUES}/{last["number"]}/rank', json={'after': bug['number']}))
        assert titles(ok(client.get(ISSUES)))[:3] == ['Ready bug with high priority', 'Last', 'Middle']

    def test_a_neighbor_of_another_priority_is_refused_naming_both(self, client):
        task = issue(client, 'Ready task without priority')
        bug = issue(client, 'Ready bug with high priority')
        detail = refused(client.post(f'{ISSUES}/{task["number"]}/rank', json={'before': bug['number']}), 409)
        assert 'no priority' in detail
        assert 'high priority' in detail

    def test_an_inherited_priority_ranks_with_its_band(self, client):
        bug = issue(client, 'Ready bug with high priority')
        child = create(client, title='Child at no priority of its own', parent=bug['number'])
        ok(client.post(f'{ISSUES}/{child["number"]}/rank', json={'before': bug['number']}))
        assert titles(ok(client.get(ISSUES)))[:2] == ['Child at no priority of its own', 'Ready bug with high priority']

    def test_one_neighbor_exactly(self, client):
        bug = issue(client, 'Ready bug with high priority')
        refused(client.post(f'{ISSUES}/{bug["number"]}/rank', json={}), 422)

    def test_an_issue_is_not_ranked_beside_itself(self, client):
        bug = issue(client, 'Ready bug with high priority')
        refused(client.post(f'{ISSUES}/{bug["number"]}/rank', json={'before': bug['number']}), 422)


class TestComments:
    def test_comments_read_oldest_first_and_are_counted(self, client):
        bug = issue(client, 'Ready bug with high priority')
        for body in ('Claimed', 'Halfway', 'Shipped'):
            ok(client.post(f'{ISSUES}/{bug["number"]}/comments', json={'body': body, 'author': 'agent'}), status.HTTP_201_CREATED)
        assert [c['body'] for c in ok(client.get(f'{ISSUES}/{bug["number"]}/comments'))] == ['Claimed', 'Halfway', 'Shipped']
        assert len(ok(client.get(f'{ISSUES}/{bug["number"]}/comments', params={'limit': 1}))) == 1
        assert issue(client, bug['title'])['comment_count'] == 3

    def test_a_comment_is_deleted_only_through_its_own_issue(self, client):
        bug = issue(client, 'Ready bug with high priority')
        task = issue(client, 'Ready task without priority')
        comment = ok(client.post(f'{ISSUES}/{bug["number"]}/comments', json={'body': 'Note'}), status.HTTP_201_CREATED)
        refused(client.delete(f'{ISSUES}/{task["number"]}/comments/{comment["id"]}'), 404)
        response = client.delete(f'{ISSUES}/{bug["number"]}/comments/{comment["id"]}')
        assert response.status_code == status.HTTP_204_NO_CONTENT, show_status_and_response(response)

    def test_a_blank_comment_is_a_422(self, client):
        bug = issue(client, 'Ready bug with high priority')
        refused(client.post(f'{ISSUES}/{bug["number"]}/comments', json={'body': '   '}), 422)


def test_the_vocabulary_lists_values_in_lifecycle_order(client):
    vocabulary = ok(client.get(f'{ISSUES}/vocabulary'))
    assert vocabulary['statuses'] == ['triage', 'open', 'in_progress', 'completed', 'canceled']
    assert vocabulary['priorities'][0] == {'value': 0, 'name': 'none'}
    assert [label['slug'] for label in vocabulary['labels']] == ['area-api', 'area-cli', 'needs-design']
