"""Create and edit pursuits, the checks each write makes, and the read shape with each idea and initiative in it."""

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
from slate.models.pursuit import TERMINAL_PURSUIT_STATUSES
from slate.models.record import Kind
from slate.services.record_parts import record_parts
from slate.services.record_pursuits import members_of
from slate.services.record_refs import record_number
from slate.services.vocabulary import validate_lookup


def validate_status(pursuit_status: str, session: Session) -> None:
    validate_lookup(session, models.PursuitStatus, pursuit_status, 'pursuit status')


def require_reason_when_dropped(pursuit_status: str, reason: str | None) -> None:
    """A dropped pursuit says why, so nobody takes it up again without reading the reason."""
    if pursuit_status == 'dropped' and not reason:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail='Dropping a pursuit requires a reason. Say why it is dropped.',
        )


def refuse_number_shaped_name(name: str) -> None:
    """A name that reads as `N` or `#N` would resolve to the record holding that number."""
    if (number := record_number(name)) is not None:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail=f'{name!r} reads as the number {number}, so it cannot name a pursuit. Name it in words.',
        )


def ensure_active_name_available(session: Session, name: str, exclude_id: UUID | None = None) -> None:
    """Only active pursuits hold a name, so a finished one never blocks taking up a new one."""
    query = select(models.Pursuit).where(models.Pursuit.name == name, models.Pursuit.status == 'active')
    if exclude_id is not None:
        query = query.where(models.Pursuit.id != exclude_id)
    if (existing := session.scalars(query).first()) is not None:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'An active pursuit named {name!r} already exists (#{existing.number}).',
        )


def listing_order() -> tuple[ColumnElement[Any], ...]:
    """Active pursuits, then someday, completed and dropped ones, finished ones latest first and the rest oldest first."""
    status_class = case(
        (models.Pursuit.status == 'active', 0),
        (models.Pursuit.status == 'someday', 1),
        (models.Pursuit.status == 'completed', 2),
        else_=3,
    )
    return status_class.asc(), models.Pursuit.closed_ts.desc().nullsfirst(), models.Pursuit.number.asc()


def new_pursuit(session: Session, pursuit: schemas.PursuitCreate) -> models.Pursuit:
    """Add a pursuit after every check a create makes."""
    refuse_number_shaped_name(pursuit.name)
    validate_status(pursuit.status, session)
    require_reason_when_dropped(pursuit.status, pursuit.status_reason)
    if pursuit.status == 'active':
        ensure_active_name_available(session, pursuit.name)
    db_obj = models.Pursuit(
        name=pursuit.name,
        summary=pursuit.summary,
        description=pursuit.description,
        measure=pursuit.measure,
        status=pursuit.status,
        status_reason=pursuit.status_reason,
    )
    if db_obj.status in TERMINAL_PURSUIT_STATUSES:
        db_obj.closed_ts = dt.datetime.now(dt.UTC)
    session.add(db_obj)
    session.flush()
    return db_obj


def edit_pursuit(session: Session, pursuit: models.Pursuit, update: schemas.PursuitUpdate) -> None:
    """Apply an edit, stamping `closed_ts` on the move into a terminal status and clearing it and the reason on any other move."""
    update_data = update.model_dump(exclude_unset=True)
    if 'name' in update_data:
        refuse_number_shaped_name(update_data['name'])
    resulting_status = update_data.get('status', pursuit.status)
    resulting_name = update_data.get('name', pursuit.name)
    if resulting_status == 'active' and (resulting_name != pursuit.name or pursuit.status != 'active'):
        ensure_active_name_available(session, resulting_name, exclude_id=pursuit.id)
    reason = update_data.get('status_reason', pursuit.status_reason)
    if (new_status := update_data.get('status')) is None:
        require_reason_when_dropped(pursuit.status, reason)
    else:
        validate_status(new_status, session)
        require_reason_when_dropped(new_status, reason)
        if new_status not in TERMINAL_PURSUIT_STATUSES:
            update_data['status_reason'] = None
            update_data['closed_ts'] = None
        elif new_status != pursuit.status:
            update_data['closed_ts'] = dt.datetime.now(dt.UTC)
    for attr, value in update_data.items():
        setattr(pursuit, attr, value)


def pursuit_views(session: Session, pursuits: Sequence[models.Pursuit]) -> list[schemas.Pursuit]:
    """Each pursuit with every idea and initiative in it, split by kind."""
    members = members_of(session, [pursuit.id for pursuit in pursuits])
    views = []
    for pursuit in pursuits:
        held = members.get(pursuit.id, [])
        views.append(
            schemas.Pursuit(
                id=pursuit.id,
                number=pursuit.number,
                name=pursuit.name,
                summary=pursuit.summary,
                description=pursuit.description,
                measure=pursuit.measure,
                status=pursuit.status,
                status_reason=pursuit.status_reason,
                created_ts=pursuit.created_ts,
                closed_ts=pursuit.closed_ts,
                ideas=[member for member in held if member.kind == Kind.IDEA],
                initiatives=[member for member in held if member.kind == Kind.INITIATIVE],
            )
        )
    return views


def pursuit_view(session: Session, pursuit: models.Pursuit) -> schemas.Pursuit:
    return pursuit_views(session, [pursuit])[0]


def pursuit_detail(session: Session, pursuit: models.Pursuit) -> schemas.PursuitDetail:
    """The pursuit with every claim, comment and document it has, as every kind's read carries them."""
    return schemas.PursuitDetail(
        **pursuit_view(session, pursuit).model_dump(),
        **record_parts(session, pursuit.number, dt.datetime.now(dt.UTC)).model_dump(),
    )
