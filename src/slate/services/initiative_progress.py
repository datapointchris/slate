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

import datetime as dt
from collections.abc import Sequence

from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.schemas.initiative_progress import IssueState
from slate.services.claim_views import live_claims
from slate.services.initiative_views import counted
from slate.services.issue_readiness import IssueReadiness
from slate.services.issue_readiness import measure_readiness
from slate.services.issue_views import ISSUE_LOAD_OPTIONS
from slate.services.issue_views import calendar_now
from slate.services.issue_views import issue_views
from slate.services.time_window import TimeWindow
from slate.services.time_window import apply_time_window
from slate.services.work_queue import queue_as_measured
from slate.services.work_totals import work_totals

# How many rows of the queue's head a progress read lists. `GET /queue` reads the rest.
NEXT_ROWS = 5


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


def initiatives_progress(
    session: Session, initiatives: Sequence[models.Initiative], zone: str, window: TimeWindow
) -> list[schemas.InitiativeProgress]:
    """Each initiative's progress as of now, read in `zone`, in the order given. `window` narrows only the closes."""
    today, now = calendar_now(zone)
    readiness = measure_readiness(session, today, now)
    return [initiative_progress(session, initiative, zone, now, readiness, window) for initiative in initiatives]


def initiative_progress(
    session: Session, initiative: models.Initiative, zone: str, now: dt.datetime, readiness: IssueReadiness, window: TimeWindow
) -> schemas.InitiativeProgress:
    issues = session.scalars(select(models.Issue).options(*ISSUE_LOAD_OPTIONS).where(models.Issue.initiative_id == initiative.id)).all()
    claims = live_claims(session, [issue.number for issue in issues], now)
    state_of = {issue.id: issue_state(issue, claims.get(issue.number), readiness) for issue in issues}

    counts = schemas.IssueStateCounts()
    for state in state_of.values():
        setattr(counts, state, getattr(counts, state) + 1)

    in_queue_order = sorted((issue for issue in issues if not issue.is_closed), key=readiness.sort_key)
    listed = {state: [issue for issue in in_queue_order if state_of[issue.id] == state] for state in ('running', 'waiting', 'blocked')}
    closes = (
        select(models.Issue)
        .options(*ISSUE_LOAD_OPTIONS)
        .where(models.Issue.initiative_id == initiative.id, models.Issue.closed_ts.is_not(None))
        .order_by(models.Issue.closed_ts.desc(), models.Issue.number.desc())
    )
    closed = list(session.scalars(apply_time_window(closes, models.Issue.closed_ts, window, zone=None)).all())

    shown = [*listed['running'], *listed['waiting'], *listed['blocked'], *closed]
    views = dict(zip((issue.id for issue in shown), issue_views(session, shown, zone, readiness), strict=True))
    in_initiative = schemas.QueueFilter.model_validate({'initiative': initiative.id})
    queue = queue_as_measured(session, zone, now, readiness, in_initiative, NEXT_ROWS)
    return schemas.InitiativeProgress(
        initiative=counted(session, initiative),
        states=counts,
        running=[views[issue.id] for issue in listed['running']],
        waiting=[views[issue.id] for issue in listed['waiting']],
        blocked=[views[issue.id] for issue in listed['blocked']],
        next=queue.queue,
        closed=[views[issue.id] for issue in closed],
        closed_since=window.since,
        closed_until=window.until,
        totals=work_totals(session, now, TimeWindow(), record=initiative.record),
    )
