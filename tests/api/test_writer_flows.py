"""A record keeps the flow of the work that created it, and every other row its writer keeps its writer's."""

import pytest
from fastapi import status
from sqlalchemy import select

from slate import models
from tests.util import show_status_and_response

TRACE_ID = '0af7651916cd43dd8448eb211c80319c'
TRACEPARENT = f'00-{TRACE_ID}-b7ad6b7169203331-01'
FILING_FLOW = '4bf92f3577b34da6a3ce929d0e0e4736'
OTHER_FLOW = '5a3c1e0b9d8f47e6a2b1c0d9e8f7a6b5'
SESSION = 'session-in-a-worklog'


def ok(response) -> dict:
    assert response.status_code in (status.HTTP_200_OK, status.HTTP_201_CREATED), show_status_and_response(response)
    return response.json()


def under(baggage: str | None = None) -> dict[str, str]:
    return {'traceparent': TRACEPARENT} | ({'baggage': baggage} if baggage else {})


def file_issue(client, title: str, baggage: str | None = None) -> int:
    return ok(client.post('/issues', json={'title': title}, headers=under(baggage)))['issue']['number']


def flow_of(client, number: int) -> str | None:
    return ok(client.get(f'/records/{number}'))['record']['flow_id']


@pytest.mark.parametrize(
    ('baggage', 'flow'),
    [
        (f'fleet.flow.id={FILING_FLOW}', FILING_FLOW),
        (None, TRACE_ID),
        (f'fleet.flow.id={FILING_FLOW.upper()}', TRACE_ID),
    ],
    ids=['its baggage flow', 'its trace id with no flow', 'its trace id over a malformed flow'],
)
def test_an_issue_keeps_its_filings_flow(txn_api_logged_in, baggage, flow):
    client, _ = txn_api_logged_in
    assert flow_of(client, file_issue(client, 'Follow a flow', baggage)) == flow


def test_a_session_works_under_its_worklogs_flow_while_its_hold_and_its_worklog_keep_their_own(txn_api_logged_in):
    client, session = txn_api_logged_in
    worked = file_issue(client, 'The record a session works', f'fleet.flow.id={FILING_FLOW}')

    claim = ok(client.post('/claims', json={'record': worked, 'holder': f'claude-code/{SESSION}', 'session': SESSION}, headers=under()))
    assert claim['claim']['flow_id'] == TRACE_ID
    assert ok(client.get(f'/worklogs/{worked}.1'))['flow_id'] == FILING_FLOW
    audited = session.scalars(select(models.RecordChange.flow_id).where(models.RecordChange.table_name.in_(['claims', 'worklogs']))).all()
    assert set(audited) == {TRACE_ID}

    found = file_issue(client, 'Found while working', f'fleet.session.id={SESSION}')
    assert flow_of(client, found) == FILING_FLOW
    comment = ok(
        client.post(
            '/comments',
            json={'record': found, 'body': 'Noted from the worklog', 'session': SESSION},
            headers=under(f'fleet.session.id={SESSION},fleet.flow.id={OTHER_FLOW}'),
        )
    )
    assert comment['comment']['flow_id'] == FILING_FLOW
