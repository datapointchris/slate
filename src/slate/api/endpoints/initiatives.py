import datetime as dt
from typing import Annotated
from typing import Any
from typing import Literal

import structlog
from fastapi import APIRouter
from fastapi import Depends
from fastapi import HTTPException
from fastapi import Query
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
from slate.api.request_zone import RequestZone
from slate.api.time_window import TimeWindowQuery
from slate.models.initiative import TERMINAL_INITIATIVE_STATUSES
from slate.services.claims import end_with_record
from slate.services.initiative_progress import initiatives_progress
from slate.services.initiative_views import counted
from slate.services.initiative_views import initiative_detail
from slate.services.initiative_views import initiative_views
from slate.services.initiative_views import issue_count_columns
from slate.services.initiatives import ensure_active_name_available
from slate.services.initiatives import new_initiative
from slate.services.initiatives import refuse_number_shaped_name
from slate.services.initiatives import refuse_routed_name
from slate.services.initiatives import require_reason_when_dropped
from slate.services.initiatives import validate_status
from slate.services.initiatives import work_order
from slate.services.likely_duplicates import warn_likely_duplicates
from slate.services.queue_moves import move_in_queue
from slate.services.record_deletes import name_initiative_delete
from slate.services.record_pins import clear_pin_on_close
from slate.services.record_pins import pin_record
from slate.services.record_pins import unpin_record
from slate.services.record_refs import resolve_initiative
from slate.services.record_summaries import record_summaries
from slate.services.row_limit import RowLimit
from slate.services.row_limit import apply_row_limit
from slate.services.write_effects import Effects
from slate.services.write_effects import WriteEffects
from slate.tracing.named_work import name_record

logger = structlog.get_logger()
router = APIRouter()

ALL_STATUSES = 'all'

InitiativeSort = Literal['priority', 'open', 'newest']


def path_initiative(id: str, session: DbSession) -> models.Initiative:
    """Resolve the `{id}` segment through `resolve_initiative`, which names the forms it accepts.

    The segment is a path, not a single segment, because a name may hold a
    slash: the client escapes it, and the path is decoded before routing.
    A name `ROUTED_NAMES` holds is refused, because a route reaches its path
    first.
    """
    initiative = resolve_initiative(session, id)
    name_record(initiative.number)
    return initiative


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
        Query(description='priority: the order the queue takes them in. open: most unclosed issues first. newest: latest started first.'),
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
    counts = issue_count_columns()
    open_count = counts[1]
    orders: dict[str, tuple[ColumnElement[Any], ...]] = {
        'priority': work_order(),
        'open': (open_count.desc(), *work_order()),
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
    """Create an initiative last in the queue, warning about each open record whose title reads like its name."""
    db_obj = new_initiative(session, initiative)
    name_record(db_obj.number)
    warn_likely_duplicates(session, db_obj.number, db_obj.name, None, effects)
    session.commit()
    session.refresh(db_obj)
    return effects.answer(schemas.InitiativeWritten, initiative=counted(session, db_obj))


@router.post('/{id:path}/rank', response_model=schemas.InitiativeWritten, status_code=status.HTTP_200_OK)
def rank(initiative: InitiativeFromPath, move: schemas.RecordRankMove, session: DbSession, zone: RequestZone, effects: Effects):
    """Place this initiative, with its issues, directly before or after another record of its priority in the queue."""
    move_in_queue(session, zone, initiative.record, move)
    session.commit()
    session.refresh(initiative)
    return effects.answer(schemas.InitiativeWritten, initiative=counted(session, initiative))


@router.post('/{id:path}/pin', response_model=schemas.InitiativeWritten, status_code=status.HTTP_200_OK)
def pin(initiative: InitiativeFromPath, request: schemas.RecordPin, session: DbSession, caller: CurrentCaller, effects: Effects):
    """Hold this initiative and its issues ahead of every priority, a person's choice alone, until unpinned, finished or shelved."""
    if initiative.status != 'active':
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'#{initiative.number} is a {initiative.status} initiative, so the queue has no place to pin. Reopen it first.',
        )
    pin_record(session, initiative.record, request, caller_actor(caller), effects)
    session.commit()
    session.refresh(initiative)
    return effects.answer(schemas.InitiativeWritten, initiative=counted(session, initiative))


@router.delete('/{id:path}/pin', response_model=schemas.InitiativeWritten, status_code=status.HTTP_200_OK)
def unpin(initiative: InitiativeFromPath, session: DbSession, effects: Effects):
    """Return this initiative to the place its priority and rank give it."""
    unpin_record(initiative.record, effects)
    session.commit()
    session.refresh(initiative)
    return effects.answer(schemas.InitiativeWritten, initiative=counted(session, initiative))


# Ahead of `read_one`, whose `{id:path}` would otherwise read `progress` as a name. `refuse_routed_name` keeps it from being one.
@router.get('/progress', response_model=list[schemas.InitiativeProgress], status_code=status.HTTP_200_OK)
def read_progress(
    session: DbSession,
    zone: RequestZone,
    window: TimeWindowQuery,
    initiative: Annotated[
        list[str] | None,
        Query(description='An initiative by any reference GET /initiatives/{id} accepts, repeated for several. Omitted, every active one.'),
    ] = None,
    limit: RowLimit = None,
):
    """Each initiative's progress: its issues counted in one state, those under way and blocked, the queue's head, its closes and totals.

    The initiatives named come in the order named, and with none named every active one comes in
    the order to work them. The window narrows only the closes, and a bound it leaves out leaves
    that end open. The totals cover each initiative's whole life.
    """
    if initiative:
        initiatives = [resolve_initiative(session, ref) for ref in initiative]
        initiatives = initiatives if limit is None else initiatives[:limit]
        if len(initiatives) == 1:
            name_record(initiatives[0].number)
    else:
        active = select(models.Initiative).where(models.Initiative.status == 'active').order_by(*work_order())
        initiatives = list(session.scalars(apply_row_limit(active, limit)).all())
    return initiatives_progress(session, initiatives, zone, window)


@router.get('/{id:path}', response_model=schemas.InitiativeDetail, status_code=status.HTTP_200_OK)
def read_one(initiative: InitiativeFromPath, session: DbSession):
    return initiative_detail(session, initiative)


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
    if new_status != 'active':
        clear_pin_on_close(initiative, new_status, effects)
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
        refuse_routed_name(update_data['name'])
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


@router.delete('/{id:path}', response_model=schemas.RecordDeleted, status_code=status.HTTP_200_OK)
def delete(initiative: InitiativeFromPath, session: DbSession, caller: CurrentCaller, effects: Effects):
    """Delete the initiative's record, and the initiative and its claims with it. Its issues stay, and stop inheriting its priority.

    The answer names each claim it took and each issue it left in no initiative.
    """
    deleted = record_summaries(session, [initiative.number])[initiative.number]
    name_initiative_delete(session, initiative, caller_actor(caller), dt.datetime.now(dt.UTC), effects)
    session.execute(sql_delete(models.Record).where(models.Record.number == initiative.number))
    session.commit()
    logger.info('initiative_deleted', number=deleted.number)
    return effects.answer(schemas.RecordDeleted, deleted=deleted)
