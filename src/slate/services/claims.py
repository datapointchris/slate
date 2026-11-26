"""Hold, extend, release, wait on, advance, adopt and end claims. `claim_views` reads them back.

Another holder's live claim refuses a hold, and so does a pursuit, which holds no work. A hold
names everything else worth a look through `claim_neighbors.neighbor_warner`. Each write here also
names every claim it ends beyond the one it was asked about.

A hold opens a worklog, and `end_claim` is the one way a claim ends, so its worklog and its open
wait end with it at the same moment. `advance` moves a claim to the next step's worklog, and
`adopt` ends a dead session's claim and holds its step again, each in one transaction, so no
moment between two calls leaves the record unclaimed or its step without a worklog.

A claim whose lease ran out before any write ended it ends as `expired`, at its expiry, whichever
write finds it. So a claim's history says when it stopped holding, not when someone noticed.
"""

from __future__ import annotations

import datetime as dt
from collections.abc import Mapping
from typing import cast

import structlog
from fastapi import HTTPException
from fastapi import status
from psycopg.errors import UniqueViolation
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.claim import CLAIM_ADOPTED
from slate.models.claim import ONE_UNENDED_CLAIM_PER_RECORD
from slate.models.initiative import TERMINAL_INITIATIVE_STATUSES
from slate.models.issue import CLOSED_ISSUE_STATUSES
from slate.models.record import Kind
from slate.models.worklog import WORKLOG_FINISHED
from slate.models.worklog import WORKLOG_MOVED_BACK
from slate.models.worklog import WORKLOG_STEPS
from slate.models.worklog import StepName
from slate.services import worklogs
from slate.services.claim_neighbors import neighbor_warner
from slate.services.claim_neighbors import warn_about_passed_over
from slate.services.claim_views import held_by
from slate.services.claim_views import live_claims
from slate.services.claim_views import unended_claims
from slate.services.record_summaries import record_summaries
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


def take(
    session: Session,
    number: int,
    lease: schemas.ClaimLease,
    now: dt.datetime,
    effects: WriteEffects,
    follows: models.Worklog | None = None,
) -> models.Claim:
    current = unended_claims(session, [number]).get(number)
    if current is not None:
        live = current.is_live(now)
        if live and current.holder == lease.holder:
            effects.warn(
                WriteEffectCode.ALREADY_HELD,
                f'{lease.holder} already holds #{number} until {effects.moment(current.expires_ts)}. '
                'Holding it again left the claim as it was. Extend the claim to keep it longer.' + kept_step(current, lease.step),
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
    worklog = worklogs.open_worklog(session, number, lease.holder, now, claim=claim, step=lease.step, follows=follows)
    opened = opened_line(worklog, follows)
    if lease.session is None:
        effects.did(WriteEffectCode.WORKLOG_OPENED, f'{opened}.', number)
        return claim
    effects.did(WriteEffectCode.WORKLOG_OPENED, f'{opened}, with session {lease.session} in it.', number)
    worklogs.join(session, worklog, lease.session, lease.host, lease.directory, now, effects)
    return claim


def opened_line(worklog: models.Worklog, follows: models.Worklog | None) -> str:
    """The side effect naming a worklog opened, its step, and the worklog it follows."""
    line = f'Opened worklog {worklog.name}'
    if worklog.step is not None:
        line += f' for step {worklog.step}'
    if follows is not None:
        line += f', following {follows.name}'
    return line


def kept_step(claim: models.Claim, step: str | None) -> str:
    """What the claim's worklog says of the step a hold asked for, and how to record that step where it says another."""
    worklog = claim.worklog
    if step is None or worklog is None:
        return ''
    if worklog.ended_ts is None and worklog.step == step:
        return f' Its worklog {worklog.name} carries step {step}.'
    if worklog.ended_ts is None and worklog.step is None:
        return (
            f' Its worklog {worklog.name} names no step. PATCH /worklogs/{worklog.name} with step {step} sets it, '
            'which `slate worklogs edit` sends.'
        )
    held = f'ended as {worklog.outcome}' if worklog.ended_ts is not None else f'carries step {worklog.step}'
    return (
        f' Its worklog {worklog.name} {held}. `slate claims advance {claim.record_number} --step {step}` opens one for {step} '
        'and moves the claim and this session to it.'
    )


def lost_the_race(exc: IntegrityError) -> bool:
    """True when another hold's unended claim beat this one to the record's one unended slot."""
    return isinstance(exc.orig, UniqueViolation) and exc.orig.diag.constraint_name == ONE_UNENDED_CLAIM_PER_RECORD


def hold(
    session: Session, record: models.Record, lease: schemas.ClaimLease, now: dt.datetime, today: dt.date, effects: WriteEffects
) -> models.Claim:
    """Take a claim on the record, refused by another holder's live claim, and on a pursuit, which holds no work.

    The insert runs in a savepoint. A hold racing this one past the read loses
    to the partial unique index, and only its own savepoint rolls back. Any
    other integrity failure is not a lost race, so it is raised as it is.
    """
    warn_about_neighbors = neighbor_warner(record)
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
    warn_about_neighbors(session, record.number, lease.holder, now, today, effects)
    return claim


def hold_next(
    session: Session,
    candidates: list[models.Issue],
    passed_over: Mapping[int, models.Claim],
    lease: schemas.ClaimLease,
    now: dt.datetime,
    today: dt.date,
    effects: WriteEffects,
) -> models.Claim | None:
    """Hold the first candidate no other hold takes first, so two callers never hold the same one."""
    for issue in candidates:
        try:
            return hold(session, issue.record, lease, now, today, effects)
        except ClaimRefused:
            continue
    if passed_over:
        warn_about_passed_over(session, passed_over, effects)
    else:
        effects.warn(WriteEffectCode.NOTHING_READY, 'Nothing in the ready queue matched, so nothing was held.')
    return None


def extend(session: Session, number: int, holder: str, minutes: int, now: dt.datetime, effects: WriteEffects) -> models.Claim:
    """Move the holder's own claim's expiry to `minutes` from now, never earlier. Extending is never done by a hold."""
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
    wanted = now + dt.timedelta(minutes=minutes)
    if current.expires_ts >= wanted:
        effects.warn(
            WriteEffectCode.LEASE_ALREADY_LONGER,
            f'The claim on #{number} already runs until {effects.moment(current.expires_ts)}, past {minutes} minutes from now.',
            number,
        )
        return current
    current.expires_ts = wanted
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
    worklog_id = current.worklog.id if current.worklog is not None else None
    current.waits.append(models.ClaimWait(waiting_on=waiting_on, started_ts=now, worklog_id=worklog_id))
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


class StepMoveRefused(HTTPException):
    """A move between steps that slate refuses, naming the command that would succeed or the holder."""

    def __init__(self, detail: str):
        super().__init__(status_code=status.HTTP_409_CONFLICT, detail=detail)


def refuse_moving_steps(session: Session, record: models.Record, step: str) -> None:
    """An idea moves to design by its promote, and a closed record has no step left to work."""
    number = record.number
    if record.kind == Kind.IDEA:
        raise StepMoveRefused(f'#{number} is an idea, which reaches design by becoming an initiative: `slate ideas promote {number}`.')
    summary = record_summaries(session, [number]).get(number)
    if summary is not None and summary.status in (CLOSED_ISSUE_STATUSES if record.kind == Kind.ISSUE else TERMINAL_INITIATIVE_STATUSES):
        noun = 'issues' if record.kind == Kind.ISSUE else 'initiatives'
        raise StepMoveRefused(
            f'#{number} is {summary.status}, so it has no step left to work. `slate {noun} reopen {number}` opens it again.'
        )


def advancing_claim(session: Session, number: int, holder: str, step: str, effects: WriteEffects, now: dt.datetime) -> models.Claim:
    """The holder's unended claim on the record, refused where it holds none or another holder's claim is there."""
    current = unended_claims(session, [number]).get(number)
    hold_it = f'`slate claims hold {number} --step {step}` holds it at {step}.'
    if current is None:
        raise StepMoveRefused(f'{holder} holds no claim on #{number}. {hold_it}')
    if current.holder == holder:
        return current
    if current.is_live(now):
        raise StepMoveRefused(f'#{number} is claimed by {held_by(current, effects)}, not {holder}.')
    raise StepMoveRefused(
        f"{holder} holds no claim on #{number}: {current.holder}'s ran out at {effects.moment(current.expires_ts)}. {hold_it}"
    )


def outcome_toward(left: models.Worklog | None, step: str) -> str:
    """`finished` for a move to a later step, or from a worklog of none, and `moved_back` for one to an earlier step."""
    if left is None or left.step is None or WORKLOG_STEPS.index(step) > WORKLOG_STEPS.index(left.step):
        return WORKLOG_FINISHED
    return WORKLOG_MOVED_BACK


def advance(
    session: Session, record: models.Record, request: schemas.ClaimAdvance, now: dt.datetime, effects: WriteEffects
) -> models.Claim:
    """Move the holder's claim to a new worklog of `request.step`, ending the one it is on, in the request's one transaction.

    A move to a later step ends that worklog `finished`, which checks its step. A move to an earlier
    one ends it `moved_back`, which checks nothing. The new worklog follows the one ended, and takes
    the claim and the holder's session. An open wait ends, as a resume ends it. The holder's own
    claim that ran out holds again for `request.minutes`, as an extend holds it.
    """
    number = record.number
    refuse_moving_steps(session, record, request.step)
    current = advancing_claim(session, number, request.holder, request.step, effects, now)
    left = current.worklog
    if left is not None and left.ended_ts is None and left.step == request.step:
        raise StepMoveRefused(
            f'Worklog {left.name} on #{number} already carries step {request.step}, so the claim is at it. '
            f'`slate claims resume {number}` puts this session back in it.'
        )
    if current.is_expired(now):
        effects.warn(
            WriteEffectCode.CLAIM_HAD_EXPIRED,
            f'The claim on #{number} had expired at {effects.moment(current.expires_ts)}. Nobody held #{number} since, so it holds again.',
            number,
        )
        current.expires_ts = now + dt.timedelta(minutes=request.minutes)
    waiting = current.open_wait
    if waiting is not None:
        waiting.ended_ts, waiting.ended_by = now, request.holder
        effects.did(
            WriteEffectCode.WAIT_ENDED,
            f'Ended the wait on {waiting.waiting_on!r}, which began at {effects.moment(waiting.started_ts)}.',
            number,
        )
    outcome = outcome_toward(left, request.step)
    if left is not None:
        if left.ended_ts is None:
            worklogs.end_worklog(left, outcome, now, request.holder)
            worklogs.name_worklog_ended(left, effects)
        left.claim = None
        session.flush()
    worklog = worklogs.open_worklog(session, number, request.holder, now, claim=current, step=request.step, follows=left)
    effects.did(WriteEffectCode.WORKLOG_OPENED, f'{opened_line(worklog, left)}.', number)
    origin = f' from worklog {left.name}' if left is not None else ''
    effects.did(
        WriteEffectCode.CLAIM_MOVED_TO_WORKLOG, f"{current.holder}'s claim on #{number} moved{origin} to worklog {worklog.name}.", number
    )
    if request.session is not None:
        worklogs.join(session, worklog, request.session, request.host, request.directory, now, effects)
    logger.info(
        'claim_advanced',
        record=number,
        holder=request.holder,
        step=request.step,
        left=left.name if left is not None else None,
        outcome=outcome if left is not None else None,
        worklog=worklog.name,
    )
    return current


def adopt(session: Session, record: models.Record, request: schemas.ClaimAdopt, now: dt.datetime, effects: WriteEffects) -> models.Claim:
    """End the claim `request.from_session` holds as `adopted`, and give the holder a claim at the same step, in one transaction.

    The new claim's worklog follows the one ended, so the step goes on without being checked. A claim
    that ran out has already ended as expired at its expiry, and keeps that ending. The insert runs in
    a savepoint, so a hold landing first refuses this one, as it refuses a racing hold.
    """
    number = record.number
    current = unended_claims(session, [number]).get(number)
    if current is None:
        raise StepMoveRefused(f'#{number} holds no claim, so there is none to adopt. `slate claims hold {number}` holds it.')
    if current.session != request.from_session:
        holding = f'in session {current.session}' if current.session is not None else 'in no session'
        raise StepMoveRefused(f'#{number} is claimed by {held_by(current, effects)} {holding}, not by session {request.from_session}.')
    if request.session is not None and request.session == current.session:
        raise StepMoveRefused(
            f'Session {request.session} holds the claim on #{number} already. `slate claims resume {number}` puts it back in the worklog.'
        )
    left = current.worklog
    lease = schemas.ClaimLease(
        holder=request.holder,
        minutes=request.minutes,
        session=request.session,
        host=request.host,
        directory=request.directory,
        step=cast(StepName, left.step) if left is not None else None,
    )
    attempt = effects.attempt()
    try:
        with session.begin_nested():
            if current.is_expired(now):
                ended = f'which had expired at {attempt.moment(current.expires_ts)}, so it ended as expired rather than adopted'
            else:
                ended = f'as adopted by {request.holder}'
            attempt.did(WriteEffectCode.CLAIM_ENDED, f"Ended {current.holder}'s claim on #{number}, {ended}.", number)
            end_claim(current, CLAIM_ADOPTED, now, request.holder, attempt)
            session.flush()
            claim = take(session, number, lease, now, attempt, follows=left)
    except IntegrityError as exc:
        if not lost_the_race(exc):
            raise
        raise ClaimRefused(f'#{number} was taken by a hold that landed first.') from None
    effects.merge(attempt)
    logger.info('claim_adopted', record=number, holder=request.holder, from_session=request.from_session, step=lease.step)
    return claim
