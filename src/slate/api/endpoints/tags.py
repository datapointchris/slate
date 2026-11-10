"""Tags: the short closed list of categories ideas, initiatives and issues carry. `docs/records.md` says why it is closed."""

from typing import Annotated

import structlog
from fastapi import APIRouter
from fastapi import Depends
from fastapi import HTTPException
from fastapi import status
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.api.callers import DbSession
from slate.api.exceptions import NotFoundException
from slate.services.names_near import names_near
from slate.services.record_tags import numbers_tagged
from slate.services.record_tags import tag_view
from slate.services.record_tags import tag_views
from slate.services.row_limit import RowLimit
from slate.services.write_effects import Effects
from slate.services.write_effects import WriteEffectCode

logger = structlog.get_logger()
router = APIRouter()


def path_tag(name: str, session: DbSession) -> models.Tag:
    tag = session.scalar(select(models.Tag).where(models.Tag.name == name))
    if tag is None:
        raise NotFoundException(
            'tag', name, logger, near=names_near(name, session.scalars(select(models.Tag.name).order_by(models.Tag.name)))
        )
    return tag


TagFromPath = Annotated[models.Tag, Depends(path_tag)]


def ensure_name_free(session: Session, name: str) -> None:
    if session.scalar(select(models.Tag.id).where(models.Tag.name == name)) is not None:
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=f'Tag {name!r} already exists.')


@router.get('', response_model=list[schemas.Tag], status_code=status.HTTP_200_OK)
def read_many(session: DbSession, limit: RowLimit = None):
    """Every tag in name order, with how many open records of each kind carry it themselves."""
    return tag_views(session, limit=limit)


@router.post('', response_model=schemas.TagWritten, status_code=status.HTTP_201_CREATED)
def create(tag: schemas.TagCreate, session: DbSession, effects: Effects):
    ensure_name_free(session, tag.name)
    db_obj = models.Tag(name=tag.name, description=tag.description)
    session.add(db_obj)
    session.commit()
    logger.info('tag_created', tag=tag.name)
    return effects.answer(schemas.TagWritten, tag=tag_view(session, db_obj))


@router.get('/{name}', response_model=schemas.Tag, status_code=status.HTTP_200_OK)
def read_one(tag: TagFromPath, session: DbSession):
    return tag_view(session, tag)


@router.patch('/{name}', response_model=schemas.TagWritten, status_code=status.HTTP_200_OK)
def update(tag: TagFromPath, update: schemas.TagUpdate, session: DbSession, effects: Effects):
    """Rename the tag or change its description. Every record carrying it carries it under the new name."""
    update_data = update.model_dump(exclude_unset=True)
    renamed_from = tag.name
    if (name := update_data.get('name')) is not None and name != tag.name:
        ensure_name_free(session, name)
    for attr, value in update_data.items():
        setattr(tag, attr, value)
    session.commit()
    if tag.name != renamed_from:
        logger.info('tag_renamed', tag=tag.name, renamed_from=renamed_from)
    return effects.answer(schemas.TagWritten, tag=tag_view(session, tag))


@router.delete('/{name}', response_model=schemas.TagDeleted, status_code=status.HTTP_200_OK)
def delete(tag: TagFromPath, session: DbSession, effects: Effects):
    """Delete the tag and take it off every record carrying it, closed ones included, naming each."""
    deleted = tag_view(session, tag)
    carriers = numbers_tagged(session, tag)
    for number in carriers:
        effects.did(WriteEffectCode.TAG_REMOVED, f'#{number} no longer carries {tag.name!r}.', number)
    session.delete(tag)
    session.commit()
    logger.info('tag_deleted', tag=deleted.name, carriers=len(carriers))
    return effects.answer(schemas.TagDeleted, deleted=deleted)
