"""Directions: the ways the work moves toward its vision, each closing when it gets there. `docs/records.md` says why they hold no work."""

from typing import Annotated

import structlog
from fastapi import APIRouter
from fastapi import Depends
from fastapi import Query
from fastapi import status
from sqlalchemy import select

from slate import models
from slate import schemas
from slate.api.callers import DbSession
from slate.services.directions import direction_detail
from slate.services.directions import direction_view
from slate.services.directions import direction_views
from slate.services.directions import edit_direction
from slate.services.directions import listing_order
from slate.services.directions import new_direction
from slate.services.directions import validate_status
from slate.services.initiatives import ALL_STATUSES
from slate.services.record_refs import resolve_direction
from slate.services.row_limit import RowLimit
from slate.services.row_limit import apply_row_limit
from slate.services.write_effects import Effects
from slate.tracing.named_work import name_record

logger = structlog.get_logger()
router = APIRouter()


def path_direction(ref: str, session: DbSession) -> models.Direction:
    """Resolve the `{ref}` segment: a number, a UUID or an exact name. A name may hold a slash, which the client escapes."""
    direction = resolve_direction(session, ref)
    name_record(direction.number)
    return direction


DirectionFromPath = Annotated[models.Direction, Depends(path_direction)]


@router.get('', response_model=list[schemas.Direction], status_code=status.HTTP_200_OK)
def read_many(
    session: DbSession,
    direction_status: Annotated[
        str, Query(alias='status', description="A direction status, or 'all'. The default reads the active ones.")
    ] = 'active',
    limit: RowLimit = None,
):
    """The directions at `status`, in `listing_order`, each with the ideas and initiatives in it."""
    query = select(models.Direction).order_by(*listing_order())
    if direction_status != ALL_STATUSES:
        validate_status(direction_status, session)
        query = query.where(models.Direction.status == direction_status)
    return direction_views(session, session.scalars(apply_row_limit(query, limit)).all())


@router.post('', response_model=schemas.DirectionWritten, status_code=status.HTTP_201_CREATED)
def create(direction: schemas.DirectionCreate, session: DbSession, effects: Effects):
    db_obj = new_direction(session, direction)
    name_record(db_obj.number)
    session.commit()
    session.refresh(db_obj)
    logger.info('direction_created', number=db_obj.number, direction=db_obj.name)
    return effects.answer(schemas.DirectionWritten, direction=direction_view(session, db_obj))


@router.get('/{ref:path}', response_model=schemas.DirectionDetail, status_code=status.HTTP_200_OK)
def read_one(direction: DirectionFromPath, session: DbSession):
    return direction_detail(session, direction)


@router.patch('/{ref:path}', response_model=schemas.DirectionWritten, status_code=status.HTTP_200_OK)
def update(direction: DirectionFromPath, update: schemas.DirectionUpdate, session: DbSession, effects: Effects):
    """Rename, describe, complete, drop with a reason, shelve or reopen the direction. Every record in it stays in it."""
    edit_direction(session, direction, update)
    session.commit()
    session.refresh(direction)
    return effects.answer(schemas.DirectionWritten, direction=direction_view(session, direction))
