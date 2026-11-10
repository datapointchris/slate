"""Pursuits: the conditions the work should bring about and hold, each read by its measure. `docs/records.md` says why they hold no work."""

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
from slate.services.initiatives import ALL_STATUSES
from slate.services.pursuits import edit_pursuit
from slate.services.pursuits import listing_order
from slate.services.pursuits import new_pursuit
from slate.services.pursuits import pursuit_detail
from slate.services.pursuits import pursuit_view
from slate.services.pursuits import pursuit_views
from slate.services.pursuits import validate_status
from slate.services.record_refs import resolve_pursuit
from slate.services.row_limit import RowLimit
from slate.services.row_limit import apply_row_limit
from slate.services.write_effects import Effects
from slate.tracing.named_work import name_record

logger = structlog.get_logger()
router = APIRouter()


def path_pursuit(ref: str, session: DbSession) -> models.Pursuit:
    """Resolve the `{ref}` segment: a number, a UUID or an exact name. A name may hold a slash, which the client escapes."""
    pursuit = resolve_pursuit(session, ref)
    name_record(pursuit.number)
    return pursuit


PursuitFromPath = Annotated[models.Pursuit, Depends(path_pursuit)]


@router.get('', response_model=list[schemas.Pursuit], status_code=status.HTTP_200_OK)
def read_many(
    session: DbSession,
    pursuit_status: Annotated[
        str, Query(alias='status', description="A pursuit status, or 'all'. The default reads the active ones.")
    ] = 'active',
    limit: RowLimit = None,
):
    """The pursuits at `status`, in `listing_order`, each with the ideas and initiatives in it."""
    query = select(models.Pursuit).order_by(*listing_order())
    if pursuit_status != ALL_STATUSES:
        validate_status(pursuit_status, session)
        query = query.where(models.Pursuit.status == pursuit_status)
    return pursuit_views(session, session.scalars(apply_row_limit(query, limit)).all())


@router.post('', response_model=schemas.PursuitWritten, status_code=status.HTTP_201_CREATED)
def create(pursuit: schemas.PursuitCreate, session: DbSession, effects: Effects):
    db_obj = new_pursuit(session, pursuit)
    name_record(db_obj.number)
    session.commit()
    session.refresh(db_obj)
    logger.info('pursuit_created', number=db_obj.number, pursuit=db_obj.name)
    return effects.answer(schemas.PursuitWritten, pursuit=pursuit_view(session, db_obj))


@router.get('/{ref:path}', response_model=schemas.PursuitDetail, status_code=status.HTTP_200_OK)
def read_one(pursuit: PursuitFromPath, session: DbSession):
    return pursuit_detail(session, pursuit)


@router.patch('/{ref:path}', response_model=schemas.PursuitWritten, status_code=status.HTTP_200_OK)
def update(pursuit: PursuitFromPath, update: schemas.PursuitUpdate, session: DbSession, effects: Effects):
    """Rename, describe, measure, complete, drop with a reason, shelve or reopen the pursuit. Every record in it stays in it."""
    edit_pursuit(session, pursuit, update)
    session.commit()
    session.refresh(pursuit)
    return effects.answer(schemas.PursuitWritten, pursuit=pursuit_view(session, pursuit))
