"""Create and edit directions, the checks each write makes, and the read shape with each idea and initiative in it."""

from __future__ import annotations

import datetime as dt
from collections.abc import Sequence
from typing import Any
from uuid import UUID

from fastapi import HTTPException
from fastapi import status
from sqlalchemy import ColumnElement
from sqlalchemy import case
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.direction import TERMINAL_DIRECTION_STATUSES
from slate.models.record import Kind
from slate.services.record_directions import members_of
from slate.services.record_parts import record_parts
from slate.services.record_refs import record_number
from slate.services.vocabulary import validate_lookup


def validate_status(direction_status: str, session: Session) -> None:
    validate_lookup(session, models.DirectionStatus, direction_status, 'direction status')


def require_reason_when_dropped(direction_status: str, reason: str | None) -> None:
    """A dropped direction says why, so nobody takes it up again without reading the reason."""
    if direction_status == 'dropped' and not reason:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail='Dropping a direction requires a reason. Say why it is dropped.',
        )


def refuse_number_shaped_name(name: str) -> None:
    """A name that reads as `N` or `#N` would resolve to the record holding that number."""
    if (number := record_number(name)) is not None:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail=f'{name!r} reads as the number {number}, so it cannot name a direction. Name it in words.',
        )


def ensure_active_name_available(session: Session, name: str, exclude_id: UUID | None = None) -> None:
    """Only active directions hold a name, so a finished one never blocks taking up a new one."""
    query = select(models.Direction).where(models.Direction.name == name, models.Direction.status == 'active')
    if exclude_id is not None:
        query = query.where(models.Direction.id != exclude_id)
    if (existing := session.scalars(query).first()) is not None:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'An active direction named {name!r} already exists (#{existing.number}).',
        )


def listing_order() -> tuple[ColumnElement[Any], ...]:
    """Active directions, then someday, completed and dropped ones, finished ones latest first and the rest oldest first."""
    status_class = case(
        (models.Direction.status == 'active', 0),
        (models.Direction.status == 'someday', 1),
        (models.Direction.status == 'completed', 2),
        else_=3,
    )
    return status_class.asc(), models.Direction.closed_ts.desc().nullsfirst(), models.Direction.number.asc()


def new_direction(session: Session, direction: schemas.DirectionCreate) -> models.Direction:
    """Add a direction after every check a create makes."""
    refuse_number_shaped_name(direction.name)
    validate_status(direction.status, session)
    require_reason_when_dropped(direction.status, direction.status_reason)
    if direction.status == 'active':
        ensure_active_name_available(session, direction.name)
    db_obj = models.Direction(
        name=direction.name,
        summary=direction.summary,
        description=direction.description,
        status=direction.status,
        status_reason=direction.status_reason,
    )
    if db_obj.status in TERMINAL_DIRECTION_STATUSES:
        db_obj.closed_ts = dt.datetime.now(dt.UTC)
    session.add(db_obj)
    session.flush()
    return db_obj


def edit_direction(session: Session, direction: models.Direction, update: schemas.DirectionUpdate) -> None:
    """Apply an edit, stamping `closed_ts` on the move into a terminal status and clearing it and the reason on any other move."""
    update_data = update.model_dump(exclude_unset=True)
    if 'name' in update_data:
        refuse_number_shaped_name(update_data['name'])
    resulting_status = update_data.get('status', direction.status)
    resulting_name = update_data.get('name', direction.name)
    if resulting_status == 'active' and (resulting_name != direction.name or direction.status != 'active'):
        ensure_active_name_available(session, resulting_name, exclude_id=direction.id)
    reason = update_data.get('status_reason', direction.status_reason)
    if (new_status := update_data.get('status')) is None:
        require_reason_when_dropped(direction.status, reason)
    else:
        validate_status(new_status, session)
        require_reason_when_dropped(new_status, reason)
        if new_status not in TERMINAL_DIRECTION_STATUSES:
            update_data['status_reason'] = None
            update_data['closed_ts'] = None
        elif new_status != direction.status:
            update_data['closed_ts'] = dt.datetime.now(dt.UTC)
    for attr, value in update_data.items():
        setattr(direction, attr, value)


def direction_views(session: Session, directions: Sequence[models.Direction]) -> list[schemas.Direction]:
    """Each direction with every idea and initiative in it, split by kind."""
    members = members_of(session, [direction.id for direction in directions])
    views = []
    for direction in directions:
        held = members.get(direction.id, [])
        views.append(
            schemas.Direction(
                id=direction.id,
                number=direction.number,
                name=direction.name,
                summary=direction.summary,
                description=direction.description,
                status=direction.status,
                status_reason=direction.status_reason,
                created_ts=direction.created_ts,
                closed_ts=direction.closed_ts,
                ideas=[member for member in held if member.kind == Kind.IDEA],
                initiatives=[member for member in held if member.kind == Kind.INITIATIVE],
            )
        )
    return views


def direction_view(session: Session, direction: models.Direction) -> schemas.Direction:
    return direction_views(session, [direction])[0]


def direction_detail(session: Session, direction: models.Direction) -> schemas.DirectionDetail:
    """The direction with every claim, comment and document it has, as every kind's read carries them."""
    return schemas.DirectionDetail(
        **direction_view(session, direction).model_dump(),
        **record_parts(session, direction.number, dt.datetime.now(dt.UTC)).model_dump(),
    )
