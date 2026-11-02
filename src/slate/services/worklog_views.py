"""Read worklogs back, with each one's state, stage and tokens derived from the moment of the read.

None is stored. `models.Worklog` decides the state, its stage is the furthest its links reached,
and its tokens sum the messages its sessions reported inside its spans.
"""

from __future__ import annotations

import datetime as dt
from typing import cast

from sqlalchemy.orm import Session
from sqlalchemy.orm import selectinload

from slate import models
from slate import schemas
from slate.models.worklog import STAGE_OF_LINK
from slate.models.worklog import WORKLOG_STAGES
from slate.models.worklog import LinkKindName
from slate.models.worklog import WorklogStage
from slate.schemas.worklog import WorklogState
from slate.services.claim_views import claim_view
from slate.services.record_summaries import record_summaries
from slate.services.session_usage import plus
from slate.services.session_usage import tokens_by_span
from slate.services.worklogs import open_span

WORKLOG_LOAD_OPTIONS = (
    selectinload(models.Worklog.spans),
    selectinload(models.Worklog.links),
    selectinload(models.Worklog.claim),
)


def worklog_state(worklog: models.Worklog, now: dt.datetime) -> WorklogState:
    if worklog.is_live(now):
        return 'live'
    if worklog.is_expired(now):
        return 'expired'
    return 'ended'


def worklog_stage(worklog: models.Worklog) -> WorklogStage:
    stages = (STAGE_OF_LINK[cast(LinkKindName, link.kind)] for link in worklog.links)
    return max(stages, key=WORKLOG_STAGES.index, default='started')


def worklog_view(
    worklog: models.Worklog, record: schemas.RecordSummary, span_tokens: dict[int, schemas.TokensByKind], now: dt.datetime
) -> schemas.Worklog:
    claim = worklog.claim
    spans = [
        schemas.WorklogSpan(
            session=span.session_id,
            directory=span.directory,
            started_ts=span.started_ts,
            ended_ts=span.ended_ts,
            tokens=span_tokens.get(span.id, schemas.TokensByKind()),
        )
        for span in worklog.spans
    ]
    tokens = schemas.TokensByKind()
    for span in spans:
        tokens = plus(tokens, span.tokens)
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
        tokens=tokens,
        spans=spans,
        links=[
            schemas.WorklogLink(
                kind=link.kind, ref=link.ref, repo=link.repo, session=link.session_id, linked_ts=link.linked_ts, linked_by=link.linked_by
            )
            for link in worklog.links
        ],
    )


def worklog_views(session: Session, worklogs: list[models.Worklog], now: dt.datetime) -> list[schemas.Worklog]:
    summaries = record_summaries(session, [worklog.record_number for worklog in worklogs])
    span_tokens = tokens_by_span(session, (span.id for worklog in worklogs for span in worklog.spans))
    return [worklog_view(worklog, summaries[worklog.record_number], span_tokens, now) for worklog in worklogs]


def current_worklog(session: Session, session_id: str, now: dt.datetime) -> models.Worklog | None:
    """The live worklog the session is in now, or None when it is in none, or in one whose claim ran out or ended."""
    span = open_span(session, session_id)
    if span is None or not span.worklog.is_live(now):
        return None
    return span.worklog
