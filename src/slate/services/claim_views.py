"""Read claims back, with the state each is in derived from the moment of the read.

A claim's state is never stored. It is `ended` once `ended_ts` is set, else `live` until its
`expires_ts` passes and `expired` after, so the same row reads differently a minute later.
"""

from __future__ import annotations

import datetime as dt
from collections.abc import Iterable

from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.schemas.claim import ClaimState
from slate.services.record_summaries import record_summaries
from slate.services.write_effects import WriteEffects


def held_by(claim: models.Claim, effects: WriteEffects) -> str:
    return f'{claim.holder} until {effects.moment(claim.expires_ts)}'


def claim_state(claim: models.Claim, now: dt.datetime) -> ClaimState:
    if claim.ended_ts is not None:
        return 'ended'
    return 'live' if claim.is_live(now) else 'expired'


def claim_view(claim: models.Claim, now: dt.datetime) -> schemas.Claim:
    wait = claim.open_wait
    return schemas.Claim(
        id=claim.id,
        record_number=claim.record_number,
        holder=claim.holder,
        session=claim.session,
        host=claim.host,
        directory=claim.directory,
        started_ts=claim.started_ts,
        expires_ts=claim.expires_ts,
        ended_ts=claim.ended_ts,
        ending=claim.ending,
        ended_by=claim.ended_by,
        state=claim_state(claim, now),
        worklog=claim.worklog.name if claim.worklog is not None else None,
        waiting=schemas.ClaimWait.model_validate(wait) if wait is not None else None,
    )


def claims_on_records(session: Session, claims: list[models.Claim], now: dt.datetime) -> list[schemas.ClaimOnRecord]:
    summaries = record_summaries(session, [claim.record_number for claim in claims])
    return [schemas.ClaimOnRecord(**claim_view(claim, now).model_dump(), record=summaries[claim.record_number]) for claim in claims]


def unended_claims(session: Session, numbers: Iterable[int]) -> dict[int, models.Claim]:
    """Each record's unended claim, live or expired. The partial unique index allows one per record."""
    wanted = sorted(set(numbers))
    if not wanted:
        return {}
    query = select(models.Claim).where(models.Claim.record_number.in_(wanted), models.Claim.ended_ts.is_(None))
    return {claim.record_number: claim for claim in session.scalars(query).all()}


def unended_claim_views(session: Session, numbers: Iterable[int], now: dt.datetime) -> dict[int, schemas.Claim]:
    return {number: claim_view(claim, now) for number, claim in unended_claims(session, numbers).items()}


def live_claims(session: Session, numbers: Iterable[int], now: dt.datetime) -> dict[int, models.Claim]:
    return {number: claim for number, claim in unended_claims(session, numbers).items() if claim.is_live(now)}


def live_claim_numbers(session: Session, now: dt.datetime) -> frozenset[int]:
    query = select(models.Claim.record_number).where(models.Claim.is_live(now))
    return frozenset(session.scalars(query).all())


def claim_history(session: Session, number: int, now: dt.datetime) -> list[schemas.Claim]:
    """Every claim the record has had, ended ones included, oldest first."""
    query = select(models.Claim).where(models.Claim.record_number == number).order_by(models.Claim.started_ts, models.Claim.id)
    return [claim_view(claim, now) for claim in session.scalars(query).all()]
