"""An initiative's progress: each of its issues in one state, the work under way and next, its closes, and its totals.

A state is derived on every read from the issue's status, its live claim and its readiness, and
never stored. `issue_state` gives each issue the first state that holds.

A claim outranks every unclosed state, because someone holding an issue is working it whatever
else is true of it. Triage outranks blocked, because nobody has accepted the work its blockers hold.

One readiness pass serves every initiative's states, lists and head of the queue, so a read of
any number of initiatives measures the issue graph once, and progress and `next` never disagree
about an issue.
"""

from __future__ import annotations

from collections import Counter
from collections import defaultdict
from collections.abc import Sequence
from itertools import chain
from typing import get_args
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.schemas.initiative_progress import IssueState
from slate.schemas.queue import InitiativeRow
from slate.schemas.queue import IssueRow
from slate.schemas.queue import QueueRow
from slate.services.claim_views import live_claims
from slate.services.initiative_views import counted_initiatives
from slate.services.issue_readiness import IssueReadiness
from slate.services.issue_readiness import measure_readiness
from slate.services.issue_views import ISSUE_LOAD_OPTIONS
from slate.services.issue_views import calendar_now
from slate.services.issue_views import issue_views
from slate.services.time_window import TimeWindow
from slate.services.work_queue import queue_as_measured
from slate.services.work_totals import work_totals

# How many rows of the queue's head a progress read lists. `GET /queue` reads the rest.
NEXT_ROWS = 5

# The states a progress read lists the issues of.
LISTED_STATES: tuple[IssueState, ...] = ('running', 'waiting', 'blocked')


def issue_state(issue: models.Issue, claim: models.Claim | None, readiness: IssueReadiness) -> IssueState:
    """The one state an issue counts in. `claim` is its live claim, if any."""
    if issue.status == 'completed':
        return 'completed'
    if issue.status == 'canceled':
        return 'canceled'
    if claim is not None:
        return 'waiting' if claim.open_wait is not None else 'running'
    if issue.status == 'triage':
        return 'triage'
    if issue.id in readiness.blocked:
        return 'blocked'
    if issue.id in readiness.ready:
        return 'ready'
    if issue.id in readiness.deferred:
        return 'deferred'
    if issue.id in readiness.shelved:
        return 'shelved'
    return 'open_children'


def next_rows(queue: schemas.Queue) -> dict[int, list[QueueRow]]:
    """The queue's rows under the initiative each is work in, by number, keeping their order and at most `NEXT_ROWS` of each."""
    rows_in: dict[int, list[QueueRow]] = defaultdict(list)
    for row in queue.queue:
        initiative = row.record.initiative.number if isinstance(row, IssueRow) and row.record.initiative else None
        number = row.number if isinstance(row, InitiativeRow) else initiative
        if number is not None and len(rows_in[number]) < NEXT_ROWS:
            rows_in[number].append(row)
    return rows_in


def initiatives_progress(
    session: Session, initiatives: Sequence[models.Initiative], zone: str, window: TimeWindow
) -> list[schemas.InitiativeProgress]:
    """Each initiative's progress as of now, read in `zone`, in the order given. `window` narrows only the closes.

    Only `totals` is read per initiative, because its reach is the initiative's own. Everything else
    is read once for all of them, so the queries do not grow with the rows those reads hold.
    """
    today, now = calendar_now(zone)
    readiness = measure_readiness(session, today, now)
    in_any = models.Issue.initiative_id.in_([initiative.id for initiative in initiatives])
    issues = session.scalars(select(models.Issue).options(*ISSUE_LOAD_OPTIONS).where(in_any)).all()
    claims = live_claims(session, [issue.number for issue in issues], now)
    state_of = {issue.id: issue_state(issue, claims.get(issue.number), readiness) for issue in issues}

    counts: Counter[tuple[UUID | None, IssueState]] = Counter((issue.initiative_id, state_of[issue.id]) for issue in issues)
    listed: dict[tuple[UUID | None, IssueState], list[models.Issue]] = defaultdict(list)
    for issue in sorted((issue for issue in issues if state_of[issue.id] in LISTED_STATES), key=readiness.sort_key):
        listed[issue.initiative_id, state_of[issue.id]].append(issue)
    closed: dict[UUID | None, list[models.Issue]] = defaultdict(list)
    for issue in sorted((issue for issue in issues if window.holds(issue.closed_ts)), key=lambda i: (i.closed_ts, i.number), reverse=True):
        closed[issue.initiative_id].append(issue)

    shown = [*chain.from_iterable(listed.values()), *chain.from_iterable(closed.values())]
    views = dict(zip((issue.id for issue in shown), issue_views(session, shown, zone, readiness), strict=True))

    only_one = {'initiative': initiatives[0].id} if len(initiatives) == 1 else {}
    queue = queue_as_measured(session, zone, now, readiness, schemas.QueueFilter.model_validate(only_one), None, plans_hold=False)
    next_in = next_rows(queue)

    def in_state(initiative: models.Initiative, state: IssueState) -> list[schemas.Issue]:
        return [views[issue.id] for issue in listed[initiative.id, state]]

    return [
        schemas.InitiativeProgress(
            initiative=view,
            states=schemas.IssueStateCounts(**{state: counts[initiative.id, state] for state in get_args(IssueState)}),
            running=in_state(initiative, 'running'),
            waiting=in_state(initiative, 'waiting'),
            blocked=in_state(initiative, 'blocked'),
            next=next_in[initiative.number],
            closed=[views[issue.id] for issue in closed[initiative.id]],
            closed_since=window.since,
            closed_until=window.until,
            totals=work_totals(session, now, TimeWindow(), record=initiative.record),
        )
        for initiative, view in zip(initiatives, counted_initiatives(session, initiatives), strict=True)
    ]
