"""Create initiatives, and the checks every create and edit of one makes."""

import datetime as dt
from typing import Any
from uuid import UUID

from fastapi import HTTPException
from fastapi import status
from sqlalchemy import ColumnElement
from sqlalchemy import Select
from sqlalchemy import case
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.initiative import TERMINAL_INITIATIVE_STATUSES
from slate.services.issue_readiness import NO_PRIORITY_URGENCY
from slate.services.record_refs import record_number

# The status a list read takes to answer every status.
ALL_STATUSES = 'all'


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


def work_order() -> tuple[ColumnElement[Any], ...]:
    """The order to work initiatives in: the most urgent first, no priority after every level, then by rank."""
    urgency = case((models.Initiative.priority == 0, NO_PRIORITY_URGENCY), else_=models.Initiative.priority)
    return urgency.asc(), models.Initiative.rank.asc()


def listing_order(*within_status: ColumnElement[Any]) -> tuple[ColumnElement[Any], ...]:
    """Active initiatives, then someday, completed and dropped ones, finished ones latest first and the rest as `within_status` says."""
    status_class = case(
        (models.Initiative.status == 'active', 0),
        (models.Initiative.status == 'someday', 1),
        (models.Initiative.status == 'completed', 2),
        else_=3,
    )
    return status_class.asc(), models.Initiative.closed_ts.desc().nullsfirst(), *within_status


def narrowed_to_status(query: Select, initiative_status: str, session: Session) -> Select:
    """Narrow to one status, refusing one that does not exist. `ALL_STATUSES` leaves every one."""
    if initiative_status == ALL_STATUSES:
        return query
    validate_status(initiative_status, session)
    return query.where(models.Initiative.status == initiative_status)


def listed_initiatives(query: Select, initiative_status: str, session: Session) -> Select:
    """`query` over initiatives, narrowed to a status and in the order to list them."""
    return narrowed_to_status(query, initiative_status, session).order_by(*listing_order(*work_order()))


# Each is a literal path under /initiatives, which routing reaches before an initiative named for it.
ROUTED_NAMES = frozenset({'progress', 'board', 'board/text'})


def refuse_routed_name(name: str) -> None:
    """A name a route under /initiatives takes could never be read back by name."""
    if name in ROUTED_NAMES:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail=f'{name!r} is the path GET /initiatives/{name} reads, so it cannot name an initiative. Name it in other words.',
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
    """Add an initiative after every check a create makes.

    Promoted from `idea`, it takes the idea's place in the queue. Otherwise its record goes last, as every new one does.
    """
    refuse_number_shaped_name(initiative.name)
    refuse_routed_name(initiative.name)
    validate_status(initiative.status, session)
    require_reason_when_dropped(initiative.status, initiative.status_reason)
    if initiative.status == 'active':
        ensure_active_name_available(session, initiative.name)

    db_obj = models.Initiative(
        name=initiative.name,
        description=initiative.description,
        status=initiative.status,
        status_reason=initiative.status_reason,
        priority=initiative.priority,
        autofix=initiative.autofix,
        idea=idea,
        rank=None if idea is None else idea.rank,
    )
    if db_obj.status in TERMINAL_INITIATIVE_STATUSES:
        db_obj.closed_ts = dt.datetime.now(dt.UTC)
    session.add(db_obj)
    session.flush()
    return db_obj
