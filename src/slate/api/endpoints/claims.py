"""Claims, the leases on records of any kind: held, extended and released here, and listed.

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
from slate.api.endpoints.issues import ready_issues
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
from slate.services.issue_views import calendar_now
from slate.services.record_refs import resolve_record
from slate.services.row_limit import RowLimit
from slate.services.row_limit import apply_row_limit
from slate.services.write_effects import Effects
from slate.services.write_effects import WriteEffects

logger = structlog.get_logger()
router = APIRouter()

ClaimStateFilter = Literal['live', 'expired', 'ended', 'all']


def path_record(record: str, session: DbSession) -> models.Record:
    return resolve_record(session, record)


RecordFromPath = Annotated[models.Record, Depends(path_record)]


def refuse_acting_for_another(caller: OIDCIdentity | ScopedClient, named: str) -> None:
    if isinstance(caller, ScopedClient) and named != caller.client_id:
        raise ForbiddenException(f'{Refusal.SCOPED_CLIENT_CLAIMS_AS_ITSELF}, {caller.client_id}, not {named}', logger)


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
    unended = models.Claim.ended_ts.is_(None)
    if state == 'live':
        query = query.where(unended, models.Claim.expires_ts > now)
    elif state == 'expired':
        query = query.where(unended, models.Claim.expires_ts <= now)
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
    refuse_acting_for_another(caller, request.holder)
    record = resolve_record(session, request.record)
    today, now = calendar_now(zone)
    return written(session, hold(session, record, request, now, today, effects), now, effects)


@router.post('/next', response_model=schemas.ClaimWritten, status_code=status.HTTP_200_OK)
def hold_next_claim(request: schemas.ClaimHoldNext, session: DbSession, zone: RequestZone, caller: CurrentCaller, effects: Effects):
    """Hold the head of the issue ready queue, narrowed by the queue's filters.

    Two callers asking at once never hold the same issue: the one that loses
    the insert moves on to the next. With nothing ready, `claim` is null and a
    warning says so.
    """
    refuse_acting_for_another(caller, request.holder)
    issues, _ = ready_issues(
        session,
        zone,
        repo=request.repo,
        type=request.type,
        label=request.label,
        initiative=request.initiative,
        autofixable=request.autofixable,
    )
    today, now = calendar_now(zone)
    return written(session, hold_next(session, issues, request, now, today, effects), now, effects)


@router.post('/{record}/extend', response_model=schemas.ClaimWritten, status_code=status.HTTP_200_OK)
def extend_claim(record: RecordFromPath, request: schemas.ClaimExtend, session: DbSession, caller: CurrentCaller, effects: Effects):
    """Move the holder's claim's expiry to `minutes` from now. 409 when the holder holds no claim on the record."""
    refuse_acting_for_another(caller, request.holder)
    now = dt.datetime.now(dt.UTC)
    return written(session, extend(session, record.number, request.holder, request.minutes, now, effects), now, effects)


@router.post('/{record}/release', response_model=schemas.ClaimWritten, status_code=status.HTTP_200_OK)
def release_claim(record: RecordFromPath, request: schemas.ClaimRelease, session: DbSession, caller: CurrentCaller, effects: Effects):
    """End the record's claim as released. A record holding none answers a warning, not an error.

    A person may release another holder's claim, and the answer warns that it
    did. A scoped client releases only a claim its own client id holds.
    """
    refuse_acting_for_another(caller, request.released_by)
    if isinstance(caller, ScopedClient):
        current = unended_claims(session, [record.number]).get(record.number)
        if current is not None and current.holder != caller.client_id:
            raise ForbiddenException(f'{Refusal.SCOPED_CLIENT_RELEASES_ITS_OWN}; #{record.number} is claimed by {current.holder}', logger)
    now = dt.datetime.now(dt.UTC)
    return written(session, release(session, record.number, request.released_by, now, effects), now, effects)
