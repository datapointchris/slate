"""Setting and ending a focus, and reading one as a client sees it. `models/focus.py` says what a focus holds.

Every set and end takes one transaction-scoped advisory lock before it reads the unended focus.
Two sets racing then run one after the other, and the later replaces the earlier, where without it
the later would meet the partial unique index and fail.
"""

from __future__ import annotations

import datetime as dt
from collections import Counter
from collections.abc import Sequence
from decimal import Decimal

import structlog
from fastapi import HTTPException
from fastapi import status
from sqlalchemy import func
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.focus import LAPSED
from slate.models.focus import REPLACED
from slate.services.record_pursuits import resolve_pursuits
from slate.services.row_limit import apply_row_limit
from slate.services.write_effects import WriteEffectCode
from slate.services.write_effects import WriteEffects

logger = structlog.get_logger()

FOCUS_LOCK = 'slate.focus'


def in_force(focus: models.Focus, today: dt.date) -> bool:
    """Whether nothing ended the focus and its last day has not passed."""
    return focus.ended_ts is None and focus.until_date >= today


def unended_focus(session: Session) -> models.Focus | None:
    return session.scalars(select(models.Focus).where(models.Focus.ended_ts.is_(None))).one_or_none()


def focus_in_force(session: Session, today: dt.date) -> models.Focus | None:
    focus = unended_focus(session)
    return focus if focus is not None and in_force(focus, today) else None


def focus_view(focus: models.Focus, today: dt.date) -> schemas.Focus:
    shares = sorted(focus.shares, key=lambda share: share.pursuit.number)
    return schemas.Focus(
        id=focus.id,
        statement=focus.statement,
        started_ts=focus.started_ts,
        until_date=focus.until_date,
        ended_ts=focus.ended_ts,
        ended_reason=focus.ended_reason,
        set_by=focus.set_by,
        active=in_force(focus, today),
        shares=[
            schemas.FocusShare(
                pursuit=schemas.PursuitSummary(number=share.pursuit.number, name=share.pursuit.name), share=float(share.share)
            )
            for share in shares
        ],
    )


def focus_history(session: Session, limit: int | None) -> Sequence[models.Focus]:
    """Every focus, the latest set first."""
    query = select(models.Focus).order_by(models.Focus.started_ts.desc(), models.Focus.id.desc())
    return session.scalars(apply_row_limit(query, limit)).all()


def refused(detail: str) -> HTTPException:
    return HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail=detail)


def named_shares(session: Session, shares: Sequence[schemas.FocusShareSet]) -> list[tuple[models.Pursuit, Decimal]]:
    """Each share's pursuit and fraction, refusing a pursuit named twice or not active, and shares summing past 1."""
    named = Counter(share.pursuit for share in shares)
    if twice := sorted(number for number, count in named.items() if count > 1):
        raise refused(f'{", ".join(f"#{number}" for number in twice)} named more than once. Name each pursuit once.')
    pursuits = resolve_pursuits(session, [share.pursuit for share in shares])
    if inactive := [pursuit for pursuit in pursuits if pursuit.status != 'active']:
        listed = ', '.join(f'#{pursuit.number} {pursuit.name} is {pursuit.status}' for pursuit in inactive)
        raise refused(f'{listed}. A share names an active pursuit.')
    total = sum((share.share for share in shares), Decimal(0))
    if total > 1:
        raise refused(f'The shares sum to {total}, past 1.')
    return list(zip(pursuits, (share.share for share in shares), strict=True))


def end(focus: models.Focus, reason: str, now: dt.datetime) -> None:
    focus.ended_ts = now
    focus.ended_reason = reason


def lapsed_since(focus: models.Focus) -> str:
    return f'Focus {focus.id} had read as ended since its until_date {focus.until_date} passed.'


def set_focus(
    session: Session, body: schemas.FocusSet, set_by: str, now: dt.datetime, today: dt.date, effects: WriteEffects
) -> models.Focus:
    """Start a focus from `now`, ending the unended one as replaced, or as lapsed where its until_date had passed."""
    if body.until_date < today:
        raise refused(f'until_date {body.until_date} is before today, {today}. A focus holds until a day not yet past.')
    shares = named_shares(session, body.shares)
    session.execute(select(func.pg_advisory_xact_lock(func.hashtext(FOCUS_LOCK))))
    current = unended_focus(session)
    if current is not None:
        if in_force(current, today):
            end(current, REPLACED, now)
            effects.did(WriteEffectCode.FOCUS_ENDED, f'Ended focus {current.id}, set {effects.moment(current.started_ts)}, as replaced.')
        else:
            end(current, LAPSED, now)
            effects.did(WriteEffectCode.FOCUS_ENDED, f'{lapsed_since(current)} It ends now as lapsed.')
        session.flush()
    focus = models.Focus(statement=body.statement, until_date=body.until_date, set_by=set_by, started_ts=now)
    session.add(focus)
    session.flush()
    session.add_all(models.FocusShare(focus_id=focus.id, pursuit_id=pursuit.id, share=share) for pursuit, share in shares)
    warn_of_shares_left_over(session, shares, effects)
    logger.info('focus_set', focus=focus.id, shares=len(shares), replaced=current.id if current is not None else None)
    return focus


def warn_of_shares_left_over(session: Session, shares: Sequence[tuple[models.Pursuit, Decimal]], effects: WriteEffects) -> None:
    """Warn where the shares name every active pursuit and sum under 1, so no pursuit takes the rest."""
    active = set(session.scalars(select(models.Pursuit.id).where(models.Pursuit.status == 'active')))
    total = sum((share for _, share in shares), Decimal(0))
    if total < 1 and active and active <= {pursuit.id for pursuit, _ in shares}:
        effects.warn(
            WriteEffectCode.SHARES_UNDER_ONE,
            f"The shares name every active pursuit and sum to {total}, so {1 - total} of the work is no pursuit's share.",
        )


def end_focus(session: Session, reason: str, now: dt.datetime, today: dt.date, effects: WriteEffects) -> models.Focus | None:
    """End the unended focus with `reason`, or warn that there is none."""
    session.execute(select(func.pg_advisory_xact_lock(func.hashtext(FOCUS_LOCK))))
    current = unended_focus(session)
    if current is None:
        effects.warn(WriteEffectCode.NO_FOCUS_TO_END, 'No focus is in force, so nothing ended.')
        return None
    if not in_force(current, today):
        effects.warn(WriteEffectCode.FOCUS_LAPSED, lapsed_since(current))
    end(current, reason, now)
    logger.info('focus_ended', focus=current.id)
    return current
