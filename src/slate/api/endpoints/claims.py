"""Claims, the leases on records of any kind: held, extended, waited on, resumed and released here, and listed.

Every write answers the claim beside its side effects and warnings. A scoped client acts only
under its own client id, and releases only a claim that id holds.
"""

import datetime as dt
from typing import Annotated
from typing import Literal

import structlog
from fastapi import APIRouter
from fastapi import Depends
from fastapi import Query
from fastapi import status
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.api.callers import CurrentCaller
from slate.api.callers import DbSession
from slate.api.callers import refuse_acting_for_another
from slate.api.exceptions import ForbiddenException
from slate.api.exceptions import Refusal
from slate.api.oidc_auth import OIDCIdentity
from slate.api.oidc_auth import ScopedClient
from slate.api.request_zone import RequestZone
from slate.services.claim_views import claims_on_records
from slate.services.claim_views import unended_claims
from slate.services.claims import extend
from slate.services.claims import hold
from slate.services.claims import hold_next
from slate.services.claims import release
from slate.services.claims import resume
from slate.services.claims import wait
from slate.services.issue_readiness import measure_readiness
from slate.services.issue_views import calendar_now
from slate.services.record_refs import resolve_record
from slate.services.row_limit import RowLimit
from slate.services.row_limit import apply_row_limit
from slate.services.work_queue import queued_issues
from slate.services.write_effects import Effects
from slate.services.write_effects import WriteEffects

logger = structlog.get_logger()
router = APIRouter()

ClaimStateFilter = Literal['live', 'expired', 'ended', 'all']


def path_record(record: str, session: DbSession) -> models.Record:
    return resolve_record(session, record)


RecordFromPath = Annotated[models.Record, Depends(path_record)]


def refuse_claiming_for_another(caller: OIDCIdentity | ScopedClient, named: str) -> None:
    refuse_acting_for_another(caller, named, Refusal.SCOPED_CLIENT_CLAIMS_AS_ITSELF)


def written(session: Session, claim: models.Claim | None, now: dt.datetime, effects: WriteEffects) -> schemas.ClaimWritten:
    session.commit()
    on_record = claims_on_records(session, [claim], now)[0] if claim is not None else None
    return effects.answer(schemas.ClaimWritten, claim=on_record)


@router.get('', response_model=list[schemas.ClaimOnRecord], status_code=status.HTTP_200_OK)
def read_many(
    session: DbSession,
    holder: str | None = Query(None, description="Claims whose holder starts with this, such as 'claude-code/'"),
    record: str | None = Query(None, description='Claims on this record, by number'),
    state: Annotated[ClaimStateFilter, Query(description='live holds its record now; expired ran out unended; ended is history')] = 'live',
    limit: RowLimit = None,
):
    """Claims, latest started first. The default reads the live ones: who holds what now."""
    now = dt.datetime.now(dt.UTC)
    query = select(models.Claim).order_by(models.Claim.started_ts.desc(), models.Claim.id.desc())
    if holder:
        query = query.where(models.Claim.holder.startswith(holder, autoescape=True))
    if record is not None:
        query = query.where(models.Claim.record_number == resolve_record(session, record).number)
    if state == 'live':
        query = query.where(models.Claim.is_live(now))
    elif state == 'expired':
        query = query.where(models.Claim.is_expired(now))
    elif state == 'ended':
        query = query.where(models.Claim.ended_ts.is_not(None))
    return claims_on_records(session, list(session.scalars(apply_row_limit(query, limit)).all()), now)


@router.post('', response_model=schemas.ClaimWritten, status_code=status.HTTP_200_OK)
def hold_claim(request: schemas.ClaimHold, session: DbSession, zone: RequestZone, caller: CurrentCaller, effects: Effects):
    """Hold a claim on one record of any kind.

    Another holder's live claim refuses it with 409. Everything else a hold
    finds is a warning: the record closed, in triage or deferred, and each
    neighbor that is open or claimed by someone else. Holding a record already
    held by the same holder changes nothing and warns, because only an extend
    moves an expiry.
    """
    refuse_claiming_for_another(caller, request.holder)
    record = resolve_record(session, request.record)
    today, now = calendar_now(zone)
    return written(session, hold(session, record, request, now, today, effects), now, effects)


@router.post('/next', response_model=schemas.ClaimWritten, status_code=status.HTTP_200_OK)
def hold_next_claim(request: schemas.ClaimHoldNext, session: DbSession, zone: RequestZone, caller: CurrentCaller, effects: Effects):
    """Hold the head of `GET /queue?step=build`, narrowed by the same filters.

    Two callers asking at once never hold the same issue: the one that loses
    the insert moves on to the next. With nothing ready, `claim` is null and a
    warning says so.
    """
    refuse_claiming_for_another(caller, request.holder)
    today, now = calendar_now(zone)
    issues = queued_issues(session, measure_readiness(session, today, now), request, 'build')
    return written(session, hold_next(session, issues, request, now, today, effects), now, effects)


@router.post('/{record}/extend', response_model=schemas.ClaimWritten, status_code=status.HTTP_200_OK)
def extend_claim(record: RecordFromPath, request: schemas.ClaimExtend, session: DbSession, caller: CurrentCaller, effects: Effects):
    """Move the holder's claim's expiry to `minutes` from now. 409 when the holder holds no claim on the record."""
    refuse_claiming_for_another(caller, request.holder)
    now = dt.datetime.now(dt.UTC)
    return written(session, extend(session, record.number, request.holder, request.minutes, now, effects), now, effects)


@router.post('/{record}/wait', response_model=schemas.ClaimWritten, status_code=status.HTTP_200_OK)
def wait_on_claim(record: RecordFromPath, request: schemas.ClaimWaitStart, session: DbSession, caller: CurrentCaller, effects: Effects):
    """Mark the holder's claim as waiting on something, which every read of it shows until the holder resumes.

    The claim still holds its record, so nobody else can take it. A new wait ends the one before it,
    named in `side_effects`. 409 when the holder holds no claim on the record, or it ran out.
    """
    refuse_claiming_for_another(caller, request.holder)
    now = dt.datetime.now(dt.UTC)
    return written(session, wait(session, record.number, request.holder, request.waiting_on, now, effects), now, effects)


@router.post('/{record}/resume', response_model=schemas.ClaimWritten, status_code=status.HTTP_200_OK)
def resume_claim(record: RecordFromPath, request: schemas.ClaimResume, session: DbSession, caller: CurrentCaller, effects: Effects):
    """End the wait on the holder's claim. A claim waiting on nothing answers a warning, not an error.

    A `session` named goes back into the claim's worklog, ending its span in any other, named in `side_effects`.
    """
    refuse_claiming_for_another(caller, request.holder)
    now = dt.datetime.now(dt.UTC)
    return written(session, resume(session, record.number, request, now, effects), now, effects)


@router.post('/{record}/release', response_model=schemas.ClaimWritten, status_code=status.HTTP_200_OK)
def release_claim(record: RecordFromPath, request: schemas.ClaimRelease, session: DbSession, caller: CurrentCaller, effects: Effects):
    """End the record's claim as released. A record holding none answers a warning, not an error.

    A person may release another holder's claim, and the answer warns that it
    did. A scoped client releases only a claim its own client id holds.
    """
    refuse_claiming_for_another(caller, request.released_by)
    if isinstance(caller, ScopedClient):
        current = unended_claims(session, [record.number]).get(record.number)
        if current is not None and current.holder != caller.client_id:
            raise ForbiddenException(f'{Refusal.SCOPED_CLIENT_RELEASES_ITS_OWN}; #{record.number} is claimed by {current.holder}', logger)
    now = dt.datetime.now(dt.UTC)
    return written(session, release(session, record.number, request.released_by, now, effects), now, effects)
