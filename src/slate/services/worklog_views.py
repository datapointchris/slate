"""Read worklogs back, with each one's state and stage derived from the moment of the read.

Neither is stored. A worklog is `ended` once `ended_ts` is set, `expired` while the claim that opened
it has run out unended, and `open` otherwise. Its stage is the furthest its links reached.
"""

from __future__ import annotations

import datetime as dt

from sqlalchemy.orm import Session
from sqlalchemy.orm import selectinload

from slate import models
from slate import schemas
from slate.schemas.worklog import WorklogStage
from slate.schemas.worklog import WorklogState
from slate.services.claim_views import claim_view
from slate.services.record_summaries import record_summaries
from slate.services.worklogs import open_span

WORKLOG_STAGES: tuple[WorklogStage, ...] = ('started', 'committed', 'pull_request', 'landed')
STAGE_OF_LINK: dict[str, WorklogStage] = {'commit': 'committed', 'pull_request': 'pull_request', 'landing': 'landed'}

WORKLOG_LOAD_OPTIONS = (
    selectinload(models.Worklog.spans),
    selectinload(models.Worklog.links),
    selectinload(models.Worklog.claim),
)


def worklog_state(worklog: models.Worklog, now: dt.datetime) -> WorklogState:
    if worklog.ended_ts is not None:
        return 'ended'
    if worklog.claim is not None and worklog.claim.is_expired(now):
        return 'expired'
    return 'open'


def worklog_stage(worklog: models.Worklog) -> WorklogStage:
    return max((STAGE_OF_LINK[link.kind] for link in worklog.links), key=WORKLOG_STAGES.index, default='started')


def worklog_view(worklog: models.Worklog, record: schemas.RecordSummary, now: dt.datetime) -> schemas.Worklog:
    claim = worklog.claim
    return schemas.Worklog(
        name=worklog.name,
        record_number=worklog.record_number,
        sequence=worklog.sequence,
        record=record,
        opened_by=worklog.opened_by,
        workflow=worklog.workflow,
        started_ts=worklog.started_ts,
        ended_ts=worklog.ended_ts,
        outcome=worklog.outcome,
        ended_by=worklog.ended_by,
        state=worklog_state(worklog, now),
        stage=worklog_stage(worklog),
        claim=claim_view(claim, now) if claim is not None else None,
        waits=[schemas.ClaimWait.model_validate(wait) for wait in claim.waits] if claim is not None else [],
        spans=[
            schemas.WorklogSpan(session=span.session_id, directory=span.directory, started_ts=span.started_ts, ended_ts=span.ended_ts)
            for span in worklog.spans
        ],
        links=[
            schemas.WorklogLink(
                kind=link.kind, ref=link.ref, repo=link.repo, session=link.session_id, linked_ts=link.linked_ts, linked_by=link.linked_by
            )
            for link in worklog.links
        ],
    )


def worklog_views(session: Session, worklogs: list[models.Worklog], now: dt.datetime) -> list[schemas.Worklog]:
    summaries = record_summaries(session, [worklog.record_number for worklog in worklogs])
    return [worklog_view(worklog, summaries[worklog.record_number], now) for worklog in worklogs]


def current_worklog(session: Session, session_id: str, now: dt.datetime) -> models.Worklog | None:
    """The open worklog the session is in now, or None when it is in none, or in one whose claim ran out."""
    span = open_span(session, session_id)
    if span is None or worklog_state(span.worklog, now) != 'open':
        return None
    return span.worklog
