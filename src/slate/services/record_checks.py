"""Checks on an issue or an initiative, and the mark asking that one be checked next.

A check is added and never edited. The mark sits on the record in the pin's shape, and the next
check on the record clears it, which the check's answer names.
"""

from __future__ import annotations

import datetime as dt
from collections.abc import Collection
from typing import Any

import structlog
from fastapi import HTTPException
from fastapi import status
from sqlalchemy import select
from sqlalchemy.dialects.postgresql import distinct_on
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.api.exceptions import with_article
from slate.models.record import Kind
from slate.services.record_refs import resolve_record
from slate.services.row_limit import apply_row_limit
from slate.services.vocabulary import validate_lookup
from slate.services.worklogs import record_session
from slate.services.write_effects import WriteEffectCode
from slate.services.write_effects import WriteEffects

logger = structlog.get_logger()

CHECKED_KINDS = (Kind.ISSUE, Kind.INITIATIVE)


def resolve_checked_record(session: Session, ref: str) -> models.Record:
    """The issue or initiative `ref` names, as `N` or `#N`. An idea holds no work yet to check, and a pursuit is a direction."""
    record = resolve_record(session, ref)
    if record.kind not in CHECKED_KINDS:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail=f'#{record.number} is {with_article(record.kind)}, and only issues and initiatives are checked.',
        )
    return record


def add_check(
    session: Session, record: models.Record, request: schemas.RecordCheckCreate, signed_in: str, effects: WriteEffects
) -> models.RecordCheck:
    validate_lookup(session, models.CheckVerdict, request.verdict, 'check verdict')
    if request.session is not None:
        record_session(session, request.session, request.host)
    check = models.RecordCheck(
        record_number=record.number,
        checked_by=signed_in,
        verdict=request.verdict,
        evidence=request.evidence,
        cites=[cite.model_dump() for cite in request.cites],
        run=request.run,
        session=request.session,
    )
    session.add(check)
    if record.check_requested_ts is not None:
        effects.did(
            WriteEffectCode.CHECK_REQUEST_CLEARED,
            f'#{record.number} lost the check request {record.check_requested_by} made, because this check answers it.',
            record.number,
        )
        clear_check_request(record)
    return check


def request_check(record: models.Record, request: schemas.CheckRequestCreate, signed_in: str, effects: WriteEffects) -> None:
    """Mark the record to be checked next. A record already marked keeps its mark and its place, and the answer warns so."""
    if record.check_requested_ts is not None:
        effects.warn(
            WriteEffectCode.ALREADY_CHECK_REQUESTED,
            f'#{record.number} was already marked for a check by {record.check_requested_by}, so that request stands.',
            record.number,
        )
        return
    record.check_requested_ts = dt.datetime.now(dt.UTC)
    record.check_requested_by = signed_in
    record.check_requested_reason = request.reason


def withdraw_check_request(record: models.Record, effects: WriteEffects) -> None:
    if record.check_requested_ts is None:
        effects.warn(WriteEffectCode.NOT_CHECK_REQUESTED, f'#{record.number} is not marked for a check, so nothing changed.', record.number)
        return
    clear_check_request(record)


def clear_check_request(record: models.Record) -> None:
    record.check_requested_ts = None
    record.check_requested_by = None
    record.check_requested_reason = None


# The columns a check request is read from, so every read of one selects the same three.
CHECK_REQUEST_COLUMNS = (models.Record.check_requested_ts, models.Record.check_requested_by, models.Record.check_requested_reason)


def check_request_of(marked: Any) -> schemas.CheckRequest | None:
    """The request a record holds, read from it or from a row selecting `CHECK_REQUEST_COLUMNS`.

    A request sets all three columns, and clearing it empties all three.
    """
    if marked.check_requested_ts is None or marked.check_requested_by is None or marked.check_requested_reason is None:
        return None
    return schemas.CheckRequest(
        requested_ts=marked.check_requested_ts, requested_by=marked.check_requested_by, reason=marked.check_requested_reason
    )


def checks_of(session: Session, number: int, limit: int | None) -> list[models.RecordCheck]:
    """The record's checks, latest first, so `limit` keeps the latest."""
    query = (
        select(models.RecordCheck)
        .where(models.RecordCheck.record_number == number)
        .order_by(models.RecordCheck.checked_ts.desc(), models.RecordCheck.id.desc())
    )
    return list(session.scalars(apply_row_limit(query, limit)).all())


def latest_checks(session: Session, numbers: Collection[int]) -> dict[int, schemas.LastCheck]:
    """Each record's latest check, by number, in one read for all of them. A record never checked is absent."""
    if not numbers:
        return {}
    query = (
        select(models.RecordCheck.record_number, models.RecordCheck.verdict, models.RecordCheck.checked_ts, models.RecordCheck.checked_by)
        .where(models.RecordCheck.record_number.in_(list(numbers)))
        .ext(distinct_on(models.RecordCheck.record_number))
        .order_by(models.RecordCheck.record_number, models.RecordCheck.checked_ts.desc(), models.RecordCheck.id.desc())
    )
    return {
        number: schemas.LastCheck(verdict=verdict, checked_ts=checked_ts, checked_by=checked_by)
        for number, verdict, checked_ts, checked_by in session.execute(query).all()
    }


def check_requests(session: Session, numbers: Collection[int]) -> dict[int, schemas.CheckRequest]:
    """Each marked record's request, by number, in one read for all of them."""
    if not numbers:
        return {}
    query = select(models.Record.number, *CHECK_REQUEST_COLUMNS).where(
        models.Record.number.in_(list(numbers)), models.Record.check_requested_ts.is_not(None)
    )
    return {row.number: request for row in session.execute(query).all() if (request := check_request_of(row)) is not None}
