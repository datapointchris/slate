"""Create initiatives, and the checks every create and edit of one makes."""

import datetime as dt
from uuid import UUID

from fastapi import HTTPException
from fastapi import status
from sqlalchemy import func
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.initiative import TERMINAL_INITIATIVE_STATUSES
from slate.services.record_refs import record_number


def validate_status(initiative_status: str, session: Session) -> None:
    if session.get(models.InitiativeStatus, initiative_status) is None:
        known = session.scalars(select(models.InitiativeStatus.name).order_by(models.InitiativeStatus.name)).all()
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail=f'Unknown initiative status {initiative_status!r}. Known statuses: {", ".join(known)}, all',
        )


def require_reason_when_dropped(initiative_status: str, reason: str | None) -> None:
    """A dropped initiative says why, so nobody re-proposes it without reading the reason."""
    if initiative_status == 'dropped' and not reason:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail='Dropping an initiative requires a reason. Say why it is dropped.',
        )


def refuse_number_shaped_name(name: str) -> None:
    """A name that reads as `N` or `#N` would resolve to the initiative holding that number."""
    if (number := record_number(name)) is not None:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail=f'{name!r} reads as the number {number}, so it cannot name an initiative. Name it in words.',
        )


def ensure_active_name_available(session: Session, name: str, exclude_id: UUID | None = None) -> None:
    """Only active initiatives hold a name, so a finished one never blocks starting a new one."""
    query = select(models.Initiative).where(models.Initiative.name == name, models.Initiative.status == 'active')
    if exclude_id is not None:
        query = query.where(models.Initiative.id != exclude_id)
    if (existing := session.scalars(query).first()) is not None:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'An active initiative named {name!r} already exists ({existing.id}).',
        )


def new_initiative(session: Session, initiative: schemas.InitiativeCreate, idea: models.Idea | None = None) -> models.Initiative:
    """Add an initiative after every check a create makes, last in the board unless placed."""
    refuse_number_shaped_name(initiative.name)
    validate_status(initiative.status, session)
    require_reason_when_dropped(initiative.status, initiative.status_reason)
    if initiative.status == 'active':
        ensure_active_name_available(session, initiative.name)

    position = initiative.position
    if position is None:
        position = (session.scalar(select(func.max(models.Initiative.position))) or 0) + 1
    db_obj = models.Initiative(
        name=initiative.name,
        description=initiative.description,
        status=initiative.status,
        status_reason=initiative.status_reason,
        priority=initiative.priority,
        position=position,
        autofix=initiative.autofix,
        idea=idea,
    )
    if db_obj.status in TERMINAL_INITIATIVE_STATUSES:
        db_obj.closed_ts = dt.datetime.now(dt.UTC)
    session.add(db_obj)
    session.flush()
    return db_obj
