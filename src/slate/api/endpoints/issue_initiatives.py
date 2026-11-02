import datetime as dt
from typing import Annotated
from typing import Any
from typing import Literal

import structlog
from fastapi import APIRouter
from fastapi import Depends
from fastapi import Query
from fastapi import Response
from fastapi import status
from sqlalchemy import ColumnElement
from sqlalchemy import case
from sqlalchemy import delete as sql_delete
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.api.callers import CurrentCaller
from slate.api.callers import DbSession
from slate.api.callers import caller_actor
from slate.models.initiative import TERMINAL_INITIATIVE_STATUSES
from slate.services.claims import end_with_record
from slate.services.initiative_views import counted
from slate.services.initiative_views import initiative_views
from slate.services.initiative_views import issue_count_columns
from slate.services.initiatives import ensure_active_name_available
from slate.services.initiatives import new_initiative
from slate.services.initiatives import refuse_number_shaped_name
from slate.services.initiatives import require_reason_when_dropped
from slate.services.initiatives import validate_status
from slate.services.issue_readiness import NO_PRIORITY_URGENCY
from slate.services.record_refs import resolve_initiative
from slate.services.row_limit import RowLimit
from slate.services.row_limit import apply_row_limit
from slate.services.write_effects import Effects
from slate.services.write_effects import WriteEffects

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
    rows = session.execute(apply_row_limit(query, limit)).all()
    return initiative_views(session, [(row[0], row[1:]) for row in rows])


@router.post('', response_model=schemas.InitiativeWritten, status_code=status.HTTP_201_CREATED)
def create(initiative: schemas.InitiativeCreate, session: DbSession, effects: Effects):
    db_obj = new_initiative(session, initiative)
    session.commit()
    session.refresh(db_obj)
    return effects.answer(schemas.InitiativeWritten, initiative=counted(session, db_obj))


@router.get('/{id:path}', response_model=schemas.Initiative, status_code=status.HTTP_200_OK)
def read_one(initiative: InitiativeFromPath, session: DbSession):
    return counted(session, initiative)


def apply_status_transition(
    initiative: models.Initiative, update_data: dict, session: Session, effects: WriteEffects, *, signed_in: str
) -> None:
    """Stamp `closed_ts` on the move into a terminal status, and clear it and the reason on any other move.

    The move into a terminal status also ends the initiative's claim as that
    status, named in `effects`. `someday` is open work set aside, so it clears
    them as `active` does.
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
        now = dt.datetime.now(dt.UTC)
        update_data['closed_ts'] = now
        end_with_record(session, initiative.number, new_status, now, signed_in, effects)


@router.patch('/{id:path}', response_model=schemas.InitiativeWritten, status_code=status.HTTP_200_OK)
def update(initiative: InitiativeFromPath, update: schemas.InitiativeUpdate, session: DbSession, caller: CurrentCaller, effects: Effects):
    update_data = update.model_dump(exclude_unset=True)
    if 'name' in update_data:
        refuse_number_shaped_name(update_data['name'])
    resulting_status = update_data.get('status', initiative.status)
    resulting_name = update_data.get('name', initiative.name)
    if resulting_status == 'active' and (resulting_name != initiative.name or initiative.status != 'active'):
        ensure_active_name_available(session, resulting_name, exclude_id=initiative.id)
    apply_status_transition(initiative, update_data, session, effects, signed_in=caller_actor(caller))
    for attr, value in update_data.items():
        setattr(initiative, attr, value)
    session.commit()
    session.refresh(initiative)
    return effects.answer(schemas.InitiativeWritten, initiative=counted(session, initiative))


@router.delete('/{id:path}', status_code=status.HTTP_204_NO_CONTENT)
def delete(initiative: InitiativeFromPath, session: DbSession):
    """Delete the initiative's record, and the initiative and its claims with it. Its issues stay, and stop inheriting its priority."""
    session.execute(sql_delete(models.Record).where(models.Record.number == initiative.number))
    session.commit()
    return Response(status_code=status.HTTP_204_NO_CONTENT)
