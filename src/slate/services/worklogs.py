"""Open, join, link and end worklogs. `worklog_views` reads them back.

Holding a claim opens a worklog through `open_worklog`, and every way a claim ends reaches
`end_worklog` through `claims.end_claim`. Nothing here ends a claim, so `claims` imports this module
and never the reverse.

A session is in one worklog at a time. `join` ends its span in any other at the moment it joins, and
names that in the answer. A span in a worklog whose claim ran out ends at the claim's expiry instead,
the moment the worklog stopped holding its record.

A claim follows its session. Where the session holding a record's live claim starts or joins another
worklog on that record, `move_claim_to` makes it the claim's, so the claim names the go under way.
`move_claim` is every move of a claim between worklogs, `claims advance` included.
"""

from __future__ import annotations

import datetime as dt

import structlog
from fastapi import HTTPException
from fastapi import status
from sqlalchemy import func
from sqlalchemy import select
from sqlalchemy.dialects.postgresql import insert
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.api.exceptions import NotFoundException
from slate.models.worklog import PREFLIGHT
from slate.models.worklog import WORKLOG_FINISHED
from slate.models.worklog import estimate_column
from slate.services.claim_views import locked_unended_claim
from slate.services.write_effects import WriteEffectCode
from slate.services.write_effects import WriteEffects

logger = structlog.get_logger()


def worklog_name_parts(ref: str) -> tuple[int, int] | None:
    """The record number and sequence `ref` names as `N.M` or `#N.M`, or None when it names none."""
    number, dot, sequence = ref.removeprefix('#').partition('.')
    if dot and all(part.isascii() and part.isdecimal() for part in (number, sequence)):
        return int(number), int(sequence)
    return None


def resolve_worklog(session: Session, ref: str) -> models.Worklog:
    """The worklog a name names, or the one a fold renamed from it."""
    parts = worklog_name_parts(ref)
    if parts is None:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail=f'{ref!r} is not a worklog name. A worklog is named <record>.<sequence>, such as 1991.2',
        )
    number, sequence = parts
    worklog = session.scalar(select(models.Worklog).where(models.Worklog.record_number == number, models.Worklog.sequence == sequence))
    if worklog is None:
        worklog = session.scalar(
            select(models.Worklog).join(models.WorklogRedirect).where(models.WorklogRedirect.name == f'{number}.{sequence}')
        )
    if worklog is None:
        raise NotFoundException('worklog', ref, logger)
    return worklog


def record_session(session: Session, session_id: str, host: str | None) -> None:
    """Record the agent session the first time slate sees it. A session already recorded keeps its row."""
    session.execute(insert(models.AgentSession).values(id=session_id, host=host).on_conflict_do_nothing(index_elements=['id']))


def open_worklog(
    session: Session,
    record_number: int,
    opened_by: str,
    now: dt.datetime,
    *,
    claim: models.Claim | None = None,
    step: str | None = None,
    follows: models.Worklog | None = None,
) -> models.Worklog:
    """Open the record's next worklog, in the record's flow, following the worklog that `follows` names.

    The record's row is locked while the sequence is counted, so two openings on one record never
    draw the same one. `FOR NO KEY UPDATE` leaves the row free for the key-share lock every foreign
    key into it takes.
    """
    locked = select(models.Record.flow_id).where(models.Record.number == record_number).with_for_update(key_share=True)
    flow_id = session.scalar(locked)
    latest = session.scalar(select(func.max(models.Worklog.sequence)).where(models.Worklog.record_number == record_number))
    worklog = models.Worklog(
        record_number=record_number,
        sequence=(latest or 0) + 1,
        claim=claim,
        opened_by=opened_by,
        step=step,
        follows_id=follows.id if follows is not None else None,
        started_ts=now,
        flow_id=flow_id,
    )
    session.add(worklog)
    session.flush()
    logger.info(
        'worklog_opened',
        worklog=worklog.name,
        opened_by=opened_by,
        claim=claim.id if claim is not None else None,
        step=step,
        follows=follows.name if follows is not None else None,
    )
    return worklog


def open_span(session: Session, session_id: str) -> models.WorklogSpan | None:
    """The span the session is in now."""
    return session.scalar(select(models.WorklogSpan).where(models.WorklogSpan.is_open_for(session_id)))


def leave(span: models.WorklogSpan, now: dt.datetime, effects: WriteEffects) -> None:
    """End the session's span in its worklog, and name it. A worklog whose claim ran out holds the span until the expiry."""
    worklog = span.worklog
    claim = worklog.claim
    span.ended_ts = span.held_until(now) or now
    message = f'Session {span.session_id} left worklog {worklog.name}.'
    if claim is not None and claim.is_expired(now):
        message = (
            f'Session {span.session_id} left worklog {worklog.name}, whose claim ran out at '
            f'{effects.moment(span.ended_ts)}, so its span ends there.'
        )
    effects.did(WriteEffectCode.SESSION_LEFT_WORKLOG, message, worklog.record_number)
    logger.info('session_left_worklog', session_id=span.session_id, worklog=worklog.name)


def join(
    session: Session,
    worklog: models.Worklog,
    session_id: str,
    host: str | None,
    directory: str | None,
    now: dt.datetime,
    effects: WriteEffects,
) -> models.WorklogSpan:
    """Put the session in the worklog from now, ending its span in any other."""
    record_session(session, session_id, host)
    current = open_span(session, session_id)
    if current is not None and current.worklog_id == worklog.id:
        effects.warn(
            WriteEffectCode.ALREADY_JOINED,
            f'Session {session_id} is in worklog {worklog.name} since {effects.moment(current.started_ts)}. Nothing changed.',
            worklog.record_number,
        )
        return current
    if current is not None:
        leave(current, now, effects)
        session.flush()
    span = models.WorklogSpan(worklog=worklog, session_id=session_id, directory=directory, started_ts=now)
    session.add(span)
    session.flush()
    logger.info('session_joined_worklog', session_id=session_id, worklog=worklog.name)
    return span


def move_claim(
    session: Session, claim: models.Claim, worklog: models.Worklog, effects: WriteEffects, *, why: str = '', opened: bool = False
) -> None:
    """Make the worklog the claim's, and name the move. Every move of a claim between worklogs is this one.

    A claim is on one worklog at a time, so the worklog it leaves lets go of it first, and keeps its
    state. A worklog `opened` for the move follows the one the claim left. `why` ends the clause
    naming the move.
    """
    left = claim.worklog
    if left is not None:
        left.claim = None
        session.flush()
        if opened and worklog.follows_id is None:
            worklog.follows_id = left.id
    worklog.claim = claim
    session.flush()
    number = worklog.record_number
    origin = f' from worklog {left.name}' if left is not None else ''
    message = f"{claim.holder}'s claim on #{number} moved{origin} to worklog {worklog.name}{why}."
    if left is not None and left.ended_ts is None:
        message += f' {left.name} stays open until it is finished or #{number} closes.'
    effects.did(WriteEffectCode.CLAIM_MOVED_TO_WORKLOG, message, number)
    logger.info('claim_moved_to_worklog', record=number, claim=claim.id, worklog=worklog.name, left=left.name if left is not None else None)


def move_claim_to(
    session: Session, worklog: models.Worklog, session_id: str, now: dt.datetime, effects: WriteEffects, *, opened: bool = False
) -> None:
    """Make the worklog its record's live claim's, where that claim's own session is the one entering it.

    The worklog the claim leaves keeps its state. One still open then ends by hand or with its
    record, as one `worklogs start` opened does. Where the session `opened` the worklog, it follows the one left.
    """
    claim = locked_unended_claim(session, worklog.record_number)
    if claim is None or not claim.is_live(now) or claim.session != session_id or worklog.claim_id is not None:
        return
    move_claim(session, claim, worklog, effects, why=f', the one its session {session_id} is in', opened=opened)


def return_to(
    session: Session,
    worklog: models.Worklog,
    session_id: str,
    host: str | None,
    directory: str | None,
    now: dt.datetime,
    effects: WriteEffects,
) -> None:
    """Put the session back in a live worklog it left, and name the move. A session already in it stays put."""
    if not worklog.is_live(now):
        return
    current = open_span(session, session_id)
    if current is not None and current.worklog_id == worklog.id:
        return
    join(session, worklog, session_id, host, directory, now, effects)
    effects.did(WriteEffectCode.SESSION_REJOINED_WORKLOG, f'Session {session_id} is back in worklog {worklog.name}.', worklog.record_number)


def end_worklog(worklog: models.Worklog, outcome: str, ended_ts: dt.datetime, ended_by: str | None) -> None:
    """End the worklog and every span still open in it, at the same moment."""
    worklog.outcome, worklog.ended_ts, worklog.ended_by = outcome, ended_ts, ended_by
    for span in worklog.spans:
        if span.ended_ts is None:
            span.ended_ts = ended_ts
    logger.info('worklog_ended', worklog=worklog.name, outcome=outcome)


def name_worklog_ended(worklog: models.Worklog, effects: WriteEffects) -> None:
    if worklog.outcome == 'expired' and worklog.ended_ts is not None:
        message = f'Ended worklog {worklog.name} as expired at {effects.moment(worklog.ended_ts)}.'
    else:
        message = f'Ended worklog {worklog.name} as {worklog.outcome}.'
    effects.did(WriteEffectCode.WORKLOG_ENDED, message, worklog.record_number)


def end_with_record(session: Session, number: int, outcome: str, now: dt.datetime, ended_by: str | None, effects: WriteEffects) -> None:
    """End each worklog still open on the record, because the record reached `outcome`, and name it.

    The record's claim is ended first, and its worklog with it, so what is left is the worklogs
    `worklogs start` opened.
    """
    session.flush()
    query = select(models.Worklog).where(models.Worklog.record_number == number, models.Worklog.ended_ts.is_(None))
    for worklog in session.scalars(query.order_by(models.Worklog.sequence)).all():
        end_worklog(worklog, outcome, now, ended_by)
        name_worklog_ended(worklog, effects)


def finish(worklog: models.Worklog, finished_by: str, now: dt.datetime, effects: WriteEffects) -> None:
    """End the worklog by hand. A claim still holding its record keeps holding it, with a warning."""
    if worklog.ended_ts is not None:
        effects.warn(
            WriteEffectCode.WORKLOG_ALREADY_ENDED,
            f'Worklog {worklog.name} ended as {worklog.outcome} at {effects.moment(worklog.ended_ts)}. Nothing changed.',
            worklog.record_number,
        )
        return
    claim = worklog.claim
    if claim is not None and claim.is_live(now):
        effects.warn(
            WriteEffectCode.CLAIM_STILL_HELD,
            f'{claim.holder} still holds #{claim.record_number}, whose hold opened {worklog.name}. '
            f'Release the claim to let #{claim.record_number} go.',
            worklog.record_number,
        )
    end_worklog(worklog, WORKLOG_FINISHED, now, finished_by)


def edit(worklog: models.Worklog, request: schemas.WorklogUpdate, effects: WriteEffects) -> None:
    """Set the step, and each estimate kind, the request names. A worklog left with an estimate off a preflight warns."""
    if 'step' in request.model_fields_set:
        worklog.step = request.step
    if 'estimate' in request.model_fields_set:
        estimate = request.estimate
        for kind in schemas.TokenEstimate.model_fields if estimate is None else estimate.model_fields_set:
            setattr(worklog, estimate_column(kind), None if estimate is None else getattr(estimate, kind))
    estimated = any(getattr(worklog, estimate_column(kind)) is not None for kind in schemas.TokenEstimate.model_fields)
    if estimated and worklog.step != PREFLIGHT:
        effects.warn(
            WriteEffectCode.ESTIMATE_OFF_PREFLIGHT,
            f'Worklog {worklog.name} performs {worklog.step or "no step"}, and `slate totals` reads an estimate from a {PREFLIGHT} '
            f'worklog alone. `slate worklogs edit {worklog.name} --step {PREFLIGHT}` makes it one.',
            worklog.record_number,
        )


def link(session: Session, worklog: models.Worklog, request: schemas.WorklogLinkAdd, now: dt.datetime, effects: WriteEffects) -> None:
    """Record a commit, pull request or landing on the worklog, once each."""
    linked = next((each for each in worklog.links if (each.kind, each.ref) == (request.kind, request.ref)), None)
    if linked is not None:
        effects.warn(
            WriteEffectCode.ALREADY_LINKED,
            f'Worklog {worklog.name} links {request.kind} {request.ref} since {effects.moment(linked.linked_ts)}. Nothing changed.',
            worklog.record_number,
        )
        return
    if request.session is not None:
        record_session(session, request.session, None)
    worklog.links.append(
        models.WorklogLink(
            kind=request.kind, ref=request.ref, repo=request.repo, session_id=request.session, linked_ts=now, linked_by=request.linked_by
        )
    )
    logger.info('worklog_linked', worklog=worklog.name, kind=request.kind, ref=request.ref)
