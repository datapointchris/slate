import datetime as dt
from typing import Annotated
from typing import Any
from typing import Literal
from uuid import UUID

import structlog
from fastapi import APIRouter
from fastapi import Depends
from fastapi import HTTPException
from fastapi import Query
from fastapi import Response
from fastapi import status
from sqlalchemy import ColumnElement
from sqlalchemy import case
from sqlalchemy import delete as sql_delete
from sqlalchemy import distinct
from sqlalchemy import func
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.api.callers import DbSession
from slate.models.initiative import TERMINAL_INITIATIVE_STATUSES
from slate.models.issue import CLOSED_ISSUE_STATUSES
from slate.services.issue_readiness import NO_PRIORITY_URGENCY
from slate.services.record_refs import record_number
from slate.services.record_refs import resolve_initiative
from slate.services.row_limit import RowLimit
from slate.services.row_limit import apply_row_limit

logger = structlog.get_logger()
router = APIRouter()

ALL_STATUSES = 'all'

InitiativeSort = Literal['priority', 'open', 'newest']


def path_initiative(id: str, session: DbSession) -> models.Initiative:
    """Resolve the `{id}` segment through `resolve_initiative`, which names the forms it accepts.

    The segment is a path, not a single segment, because a name may hold a
    slash: the client escapes it, and the path is decoded before routing.
    """
    return resolve_initiative(session, id)


InitiativeFromPath = Annotated[models.Initiative, Depends(path_initiative)]


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


def issue_count_columns():
    """Every count in one pass over the join, partitioned by whether the issue is closed and how."""
    unclosed = models.Issue.status.not_in(CLOSED_ISSUE_STATUSES)
    return (
        func.count(models.Issue.id).label('issue_count'),
        func.count(models.Issue.id).filter(unclosed).label('open_count'),
        func.count(models.Issue.id).filter(models.Issue.status == 'completed').label('completed_count'),
        func.count(models.Issue.id).filter(models.Issue.status == 'canceled').label('canceled_count'),
        func.array_agg(distinct(models.Issue.repo))
        .filter(models.Issue.status != 'canceled', models.Issue.repo.is_not(None))
        .label('repos'),
    )


def initiative_view(initiative: models.Initiative, issue_count, open_count, completed_count, canceled_count, repos) -> schemas.Initiative:
    return schemas.Initiative(
        id=initiative.id,
        number=initiative.number,
        name=initiative.name,
        description=initiative.description,
        status=initiative.status,
        status_reason=initiative.status_reason,
        priority=initiative.priority,
        position=initiative.position,
        autofix=initiative.autofix,
        created_ts=initiative.created_ts,
        closed_ts=initiative.closed_ts,
        issue_count=issue_count,
        open_count=open_count,
        completed_count=completed_count,
        canceled_count=canceled_count,
        repos=sorted(repos) if repos else [],
    )


def counted(session: Session, initiative: models.Initiative) -> schemas.Initiative:
    counts = select(*issue_count_columns()).where(models.Issue.initiative_id == initiative.id)
    return initiative_view(initiative, *session.execute(counts).one())


@router.get('', response_model=list[schemas.Initiative], status_code=status.HTTP_200_OK)
def read_many(
    session: DbSession,
    initiative_status: Annotated[
        str,
        Query(alias='status', description="An initiative status, or 'all'. Finished initiatives are hidden by default."),
    ] = 'active',
    sort: Annotated[
        InitiativeSort,
        Query(description='priority: the order to work them in. open: most unclosed issues first. newest: latest started first.'),
    ] = 'priority',
    limit: RowLimit = None,
):
    """Active initiatives, then someday, completed and dropped ones, each status in `sort`'s order.

    Finished ones come latest first whatever the sort. Priority 0 is no priority
    and sorts after every level, as it does on issues.
    """
    if initiative_status != ALL_STATUSES:
        validate_status(initiative_status, session)
    status_class = case(
        (models.Initiative.status == 'active', 0),
        (models.Initiative.status == 'someday', 1),
        (models.Initiative.status == 'completed', 2),
        else_=3,
    )
    urgency = case((models.Initiative.priority == 0, NO_PRIORITY_URGENCY), else_=models.Initiative.priority)
    counts = issue_count_columns()
    open_count = counts[1]
    orders: dict[str, tuple[ColumnElement[Any], ...]] = {
        'priority': (urgency.asc(), models.Initiative.position.asc(), models.Initiative.created_ts.asc()),
        'open': (open_count.desc(), urgency.asc(), models.Initiative.position.asc()),
        'newest': (models.Initiative.created_ts.desc(),),
    }
    within_class = orders[sort]
    query = (
        select(models.Initiative, *counts)
        .outerjoin(models.Issue, models.Issue.initiative_id == models.Initiative.id)
        .group_by(models.Initiative.id)
        .order_by(status_class.asc(), models.Initiative.closed_ts.desc().nullsfirst(), *within_class)
    )
    if initiative_status != ALL_STATUSES:
        query = query.where(models.Initiative.status == initiative_status)
    return [initiative_view(*row) for row in session.execute(apply_row_limit(query, limit)).all()]


@router.post('', response_model=schemas.Initiative, status_code=status.HTTP_201_CREATED)
def create(initiative: schemas.InitiativeCreate, session: DbSession):
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
    )
    if db_obj.status in TERMINAL_INITIATIVE_STATUSES:
        db_obj.closed_ts = dt.datetime.now(dt.UTC)
    session.add(db_obj)
    session.commit()
    session.refresh(db_obj)
    return counted(session, db_obj)


@router.get('/{id:path}', response_model=schemas.Initiative, status_code=status.HTTP_200_OK)
def read_one(initiative: InitiativeFromPath, session: DbSession):
    return counted(session, initiative)


def apply_status_transition(initiative: models.Initiative, update_data: dict, session: Session) -> None:
    """Stamp `closed_ts` on the move into a terminal status, and clear it and the reason on any other move.

    `someday` is open work set aside, so it clears them as `active` does.
    """
    new_status = update_data.get('status')
    reason = update_data.get('status_reason', initiative.status_reason)
    if new_status is None:
        require_reason_when_dropped(initiative.status, reason)
        return
    validate_status(new_status, session)
    require_reason_when_dropped(new_status, reason)
    if new_status not in TERMINAL_INITIATIVE_STATUSES:
        update_data['status_reason'] = None
        update_data['closed_ts'] = None
    elif new_status != initiative.status:
        update_data['closed_ts'] = dt.datetime.now(dt.UTC)


@router.patch('/{id:path}', response_model=schemas.Initiative, status_code=status.HTTP_200_OK)
def update(initiative: InitiativeFromPath, update: schemas.InitiativeUpdate, session: DbSession):
    update_data = update.model_dump(exclude_unset=True)
    if 'name' in update_data:
        refuse_number_shaped_name(update_data['name'])
    resulting_status = update_data.get('status', initiative.status)
    resulting_name = update_data.get('name', initiative.name)
    if resulting_status == 'active' and (resulting_name != initiative.name or initiative.status != 'active'):
        ensure_active_name_available(session, resulting_name, exclude_id=initiative.id)
    apply_status_transition(initiative, update_data, session)
    for attr, value in update_data.items():
        setattr(initiative, attr, value)
    session.commit()
    session.refresh(initiative)
    return counted(session, initiative)


@router.delete('/{id:path}', status_code=status.HTTP_204_NO_CONTENT)
def delete(initiative: InitiativeFromPath, session: DbSession):
    """Delete the initiative's record and the initiative with it. Its issues stay, and stop inheriting its priority."""
    session.execute(sql_delete(models.Record).where(models.Record.number == initiative.number))
    session.commit()
    return Response(status_code=status.HTTP_204_NO_CONTENT)
