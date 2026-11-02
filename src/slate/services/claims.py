"""Hold, extend, release and end claims. `claim_views` reads them back.

Another holder's live claim refuses a hold, and nothing else does. A hold names everything else
worth a look through `claim_neighbors.warn_about_neighbors`. Each write here also names every
claim it ends beyond the one it was asked about.

A claim whose lease ran out before any write ended it ends as `expired`, at its expiry, whichever
write finds it. So a claim's history says when it stopped holding, not when someone noticed.
"""

from __future__ import annotations

import datetime as dt

import structlog
from fastapi import HTTPException
from fastapi import status
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.services.claim_neighbors import warn_about_neighbors
from slate.services.claim_views import held_by
from slate.services.claim_views import live_claims
from slate.services.claim_views import unended_claims
from slate.services.claim_views import utc_minute
from slate.services.write_effects import WriteEffectCode
from slate.services.write_effects import WriteEffects

logger = structlog.get_logger()


class ClaimRefused(HTTPException):
    """Another holder's live claim on the record, the one conflict that refuses a hold."""

    def __init__(self, detail: str):
        super().__init__(status_code=status.HTTP_409_CONFLICT, detail=detail)


def end_claim(claim: models.Claim, ending: str, now: dt.datetime, ended_by: str | None) -> bool:
    """End the claim as `ending`, or as expired at its expiry when its lease ran out first. True when it had."""
    if claim.expires_ts <= now:
        claim.ending, claim.ended_ts, claim.ended_by = 'expired', claim.expires_ts, None
        return True
    claim.ending, claim.ended_ts, claim.ended_by = ending, now, ended_by
    return False


def end_with_record(session: Session, number: int, ending: str, now: dt.datetime, ended_by: str | None, effects: WriteEffects) -> None:
    """End the record's claim because the record reached `ending`, a closing status, and name it."""
    claim = unended_claims(session, [number]).get(number)
    if claim is None:
        return
    if end_claim(claim, ending, now, ended_by):
        message = f"Ended {claim.holder}'s claim on #{number}, which had expired at {utc_minute(claim.expires_ts)}."
    else:
        message = f"Ended {claim.holder}'s claim on #{number}, because #{number} is {ending}."
    effects.did(WriteEffectCode.CLAIM_ENDED, message, number)
    logger.info('claim_ended', record=number, holder=claim.holder, ending=claim.ending)


def take(session: Session, number: int, lease: schemas.ClaimLease, now: dt.datetime, effects: WriteEffects) -> models.Claim:
    current = unended_claims(session, [number]).get(number)
    if current is not None:
        live = current.expires_ts > now
        if live and current.holder == lease.holder:
            effects.warn(
                WriteEffectCode.ALREADY_HELD,
                f'{lease.holder} already holds #{number} until {utc_minute(current.expires_ts)}. '
                'Holding it again changed nothing. Extend the claim to keep it longer.',
                number,
            )
            return current
        if live:
            raise ClaimRefused(f'#{number} is claimed by {held_by(current)}.')
        end_claim(current, 'expired', now, None)
        effects.did(
            WriteEffectCode.EXPIRED_CLAIM_ENDED,
            f"Ended {current.holder}'s claim on #{number} as expired at {utc_minute(current.expires_ts)}, before this one started.",
            number,
        )
        session.flush()
    claim = models.Claim(
        record_number=number,
        holder=lease.holder,
        session=lease.session,
        host=lease.host,
        directory=lease.directory,
        started_ts=now,
        expires_ts=now + dt.timedelta(minutes=lease.minutes),
    )
    session.add(claim)
    session.flush()
    logger.info('claim_held', record=number, holder=lease.holder, expires_ts=claim.expires_ts.isoformat())
    return claim


def hold(
    session: Session, record: models.Record, lease: schemas.ClaimLease, now: dt.datetime, today: dt.date, effects: WriteEffects
) -> models.Claim:
    """Take a claim on the record, refused only by another holder's live claim.

    The insert runs in a savepoint. A hold racing this one past the read loses
    to the partial unique index, and only its own savepoint rolls back.
    """
    attempt = WriteEffects()
    try:
        with session.begin_nested():
            claim = take(session, record.number, lease, now, attempt)
    except IntegrityError:
        current = live_claims(session, [record.number], now).get(record.number)
        held = f'is claimed by {held_by(current)}' if current is not None else 'was taken by a hold that landed first'
        raise ClaimRefused(f'#{record.number} {held}.') from None
    effects.merge(attempt)
    warn_about_neighbors(session, record, lease.holder, now, today, effects)
    return claim


def hold_next(
    session: Session, candidates: list[models.Issue], lease: schemas.ClaimLease, now: dt.datetime, today: dt.date, effects: WriteEffects
) -> models.Claim | None:
    """Hold the first candidate no other hold takes first, so two callers never hold the same one."""
    for issue in candidates:
        try:
            return hold(session, issue.record, lease, now, today, effects)
        except ClaimRefused:
            continue
    effects.warn(WriteEffectCode.NOTHING_READY, 'Nothing in the ready queue matched, so nothing was held.')
    return None


def extend(session: Session, number: int, holder: str, minutes: int, now: dt.datetime, effects: WriteEffects) -> models.Claim:
    """Move the holder's own claim's expiry to `minutes` from now. Extending is never done by a hold."""
    current = unended_claims(session, [number]).get(number)
    if current is None:
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=f'#{number} holds no claim to extend. Hold it instead.')
    if current.holder != holder:
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=f'#{number} is claimed by {held_by(current)}, not {holder}.')
    if current.expires_ts <= now:
        effects.warn(
            WriteEffectCode.CLAIM_HAD_EXPIRED,
            f'The claim on #{number} had expired at {utc_minute(current.expires_ts)}. Nobody held #{number} since, so it holds again.',
            number,
        )
    current.expires_ts = now + dt.timedelta(minutes=minutes)
    logger.info('claim_extended', record=number, holder=holder, expires_ts=current.expires_ts.isoformat())
    return current


def release(session: Session, number: int, released_by: str, now: dt.datetime, effects: WriteEffects) -> models.Claim | None:
    """End the record's unended claim as released. A person may release another holder's, which it warns about."""
    current = unended_claims(session, [number]).get(number)
    if current is None:
        effects.warn(WriteEffectCode.NOTHING_TO_RELEASE, f'#{number} holds no claim, so nothing was released.', number)
        return None
    if current.holder != released_by:
        effects.warn(
            WriteEffectCode.RELEASED_ANOTHERS_CLAIM,
            f"Released {current.holder}'s claim on #{number}, which {released_by} did not hold.",
            number,
        )
    if end_claim(current, 'released', now, released_by):
        effects.did(
            WriteEffectCode.CLAIM_ENDED,
            f'The claim on #{number} had expired at {utc_minute(current.expires_ts)}, so it ended as expired rather than released.',
            number,
        )
    logger.info('claim_released', record=number, holder=current.holder, released_by=released_by, ending=current.ending)
    return current
