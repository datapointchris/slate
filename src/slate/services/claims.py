"""Hold, extend, release, wait on and end claims. `claim_views` reads them back.

Another holder's live claim refuses a hold, and nothing else does. A hold names everything else
worth a look through `claim_neighbors.warn_about_neighbors`. Each write here also names every
claim it ends beyond the one it was asked about.

A hold opens a worklog, and `end_claim` is the one way a claim ends, so its worklog and its open
wait end with it at the same moment.

A claim whose lease ran out before any write ended it ends as `expired`, at its expiry, whichever
write finds it. So a claim's history says when it stopped holding, not when someone noticed.
"""

from __future__ import annotations

import datetime as dt

import structlog
from fastapi import HTTPException
from fastapi import status
from psycopg.errors import UniqueViolation
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.claim import ONE_UNENDED_CLAIM_PER_RECORD
from slate.services import worklogs
from slate.services.claim_neighbors import warn_about_neighbors
from slate.services.claim_views import held_by
from slate.services.claim_views import live_claims
from slate.services.claim_views import unended_claims
from slate.services.write_effects import WriteEffectCode
from slate.services.write_effects import WriteEffects

logger = structlog.get_logger()


class ClaimRefused(HTTPException):
    """Another holder's live claim on the record, the one conflict that refuses a hold."""

    def __init__(self, detail: str):
        super().__init__(status_code=status.HTTP_409_CONFLICT, detail=detail)


def end_claim(claim: models.Claim, ending: str, now: dt.datetime, ended_by: str | None, effects: WriteEffects) -> None:
    """End the claim as `ending`, or as expired at its expiry when its lease ran out first.

    Its open wait and its worklog end at the same moment, and the worklog's ending is named. The
    caller names the claim's own ending, because only it knows why the claim ended.
    """
    ended_ts = claim.held_until(now)
    if claim.is_expired(now):
        ending, ended_by = 'expired', None
    claim.ending, claim.ended_ts, claim.ended_by = ending, ended_ts, ended_by
    wait = claim.open_wait
    if wait is not None:
        wait.ended_ts, wait.ended_by = ended_ts, ended_by
    worklog = claim.worklog
    if worklog is not None and worklog.ended_ts is None:
        worklogs.end_worklog(worklog, ending, ended_ts, ended_by)
        worklogs.name_worklog_ended(worklog, effects)


def end_with_record(session: Session, number: int, ending: str, now: dt.datetime, ended_by: str | None, effects: WriteEffects) -> None:
    """End the record's claim and its open worklogs because the record reached `ending`, a closing status or `promoted`, and name each."""
    claim = unended_claims(session, [number]).get(number)
    if claim is not None:
        if claim.is_expired(now):
            message = f"Ended {claim.holder}'s claim on #{number}, which had expired at {effects.moment(claim.expires_ts)}."
        else:
            message = f"Ended {claim.holder}'s claim on #{number}, because #{number} is {ending}."
        effects.did(WriteEffectCode.CLAIM_ENDED, message, number)
        end_claim(claim, ending, now, ended_by, effects)
        logger.info('claim_ended', record=number, holder=claim.holder, ending=claim.ending)
    worklogs.end_with_record(session, number, ending, now, ended_by, effects)


def take(session: Session, number: int, lease: schemas.ClaimLease, now: dt.datetime, effects: WriteEffects) -> models.Claim:
    current = unended_claims(session, [number]).get(number)
    if current is not None:
        live = current.is_live(now)
        if live and current.holder == lease.holder:
            effects.warn(
                WriteEffectCode.ALREADY_HELD,
                f'{lease.holder} already holds #{number} until {effects.moment(current.expires_ts)}. '
                'Holding it again left the claim as it was. Extend the claim to keep it longer.',
                number,
            )
            if lease.session is not None and current.worklog is not None:
                worklogs.return_to(session, current.worklog, lease.session, lease.host, lease.directory, now, effects)
            return current
        if live:
            raise ClaimRefused(f'#{number} is claimed by {held_by(current, effects)}.')
        effects.did(
            WriteEffectCode.EXPIRED_CLAIM_ENDED,
            f"Ended {current.holder}'s claim on #{number} as expired at {effects.moment(current.expires_ts)}, before this one started.",
            number,
        )
        end_claim(current, 'expired', now, None, effects)
        session.flush()
    if lease.session is not None:
        worklogs.record_session(session, lease.session, lease.host)
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
    worklog = worklogs.open_worklog(session, number, lease.holder, now, claim=claim)
    if lease.session is None:
        effects.did(WriteEffectCode.WORKLOG_OPENED, f'Opened worklog {worklog.name}.', number)
        return claim
    effects.did(WriteEffectCode.WORKLOG_OPENED, f'Opened worklog {worklog.name}, with session {lease.session} in it.', number)
    worklogs.join(session, worklog, lease.session, lease.host, lease.directory, now, effects)
    return claim


def lost_the_race(exc: IntegrityError) -> bool:
    """True when another hold's unended claim beat this one to the record's one unended slot."""
    return isinstance(exc.orig, UniqueViolation) and exc.orig.diag.constraint_name == ONE_UNENDED_CLAIM_PER_RECORD


def hold(
    session: Session, record: models.Record, lease: schemas.ClaimLease, now: dt.datetime, today: dt.date, effects: WriteEffects
) -> models.Claim:
    """Take a claim on the record, refused only by another holder's live claim.

    The insert runs in a savepoint. A hold racing this one past the read loses
    to the partial unique index, and only its own savepoint rolls back. Any
    other integrity failure is not a lost race, so it is raised as it is.
    """
    attempt = effects.attempt()
    try:
        with session.begin_nested():
            claim = take(session, record.number, lease, now, attempt)
    except IntegrityError as exc:
        if not lost_the_race(exc):
            raise
        current = live_claims(session, [record.number], now).get(record.number)
        held = f'is claimed by {held_by(current, effects)}' if current is not None else 'was taken by a hold that landed first'
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
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT, detail=f'#{number} is claimed by {held_by(current, effects)}, not {holder}.'
        )
    if current.is_expired(now):
        effects.warn(
            WriteEffectCode.CLAIM_HAD_EXPIRED,
            f'The claim on #{number} had expired at {effects.moment(current.expires_ts)}. Nobody held #{number} since, so it holds again.',
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
    if current.is_expired(now):
        effects.did(
            WriteEffectCode.CLAIM_ENDED,
            f'The claim on #{number} had expired at {effects.moment(current.expires_ts)}, so it ended as expired rather than released.',
            number,
        )
    end_claim(current, 'released', now, released_by, effects)
    logger.info('claim_released', record=number, holder=current.holder, released_by=released_by, ending=current.ending)
    return current


def holders_claim(session: Session, number: int, holder: str, effects: WriteEffects) -> models.Claim:
    """The holder's unended claim on the record, refused with 409 when the record has none or another holds it."""
    current = unended_claims(session, [number]).get(number)
    if current is None:
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=f'#{number} holds no claim. Hold it first.')
    if current.holder != holder:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT, detail=f'#{number} is claimed by {held_by(current, effects)}, not {holder}.'
        )
    return current


def wait(session: Session, number: int, holder: str, waiting_on: str, now: dt.datetime, effects: WriteEffects) -> models.Claim:
    """Mark the holder's claim as waiting on something. The claim still holds its record while it waits."""
    current = holders_claim(session, number, holder, effects)
    if current.is_expired(now):
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'The claim on #{number} ran out at {effects.moment(current.expires_ts)}. Extend it, then wait.',
        )
    waiting = current.open_wait
    if waiting is not None and waiting.waiting_on == waiting_on:
        effects.warn(
            WriteEffectCode.ALREADY_WAITING,
            f'The claim on #{number} waits on {waiting_on!r} since {effects.moment(waiting.started_ts)}. Nothing changed.',
            number,
        )
        return current
    if waiting is not None:
        waiting.ended_ts, waiting.ended_by = now, holder
        effects.did(
            WriteEffectCode.WAIT_ENDED,
            f'Ended the wait on {waiting.waiting_on!r}, which began at {effects.moment(waiting.started_ts)}.',
            number,
        )
        session.flush()
    current.waits.append(models.ClaimWait(waiting_on=waiting_on, started_ts=now))
    logger.info('claim_waiting', record=number, holder=holder, waiting_on=waiting_on)
    return current


def resume(session: Session, number: int, request: schemas.ClaimResume, now: dt.datetime, effects: WriteEffects) -> models.Claim:
    """End the wait on the holder's claim, which reads as worked again, and put the holder's session back in its worklog."""
    current = holders_claim(session, number, request.holder, effects)
    waiting = current.open_wait
    if waiting is None:
        effects.warn(WriteEffectCode.NOT_WAITING, f'The claim on #{number} waits on nothing, so no wait ended.', number)
    else:
        waiting.ended_ts, waiting.ended_by = now, request.holder
        logger.info('claim_resumed', record=number, holder=request.holder, waited_on=waiting.waiting_on)
    if request.session is not None and current.worklog is not None:
        worklogs.return_to(session, current.worklog, request.session, request.host, request.directory, now, effects)
    return current
