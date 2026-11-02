from typing import Annotated

import structlog
from fastapi import APIRouter
from fastapi import Depends
from fastapi import HTTPException
from fastapi import status
from sqlalchemy import select
from sqlalchemy.orm import Session
from sqlalchemy.orm import aliased

from slate import models
from slate import schemas
from slate.api.callers import DbSession
from slate.services.issue_views import label_views
from slate.services.record_refs import resolve_label
from slate.services.write_effects import Effects
from slate.services.write_effects import WriteEffectCode

logger = structlog.get_logger()
router = APIRouter()


def path_label(slug: str, session: DbSession) -> models.IssueLabel:
    return resolve_label(session, slug)


LabelFromPath = Annotated[models.IssueLabel, Depends(path_label)]


def view(session: Session, label: models.IssueLabel) -> schemas.IssueLabel:
    return label_views(session, label.slug)[0]


def ensure_group_holds(session: Session, label: models.IssueLabel, group_slug: str) -> None:
    """Refuse moving a label into a group when an issue already carries another label from it.

    Labels in one group exclude each other, so the move would leave those issues
    holding two. The refusal names them, so they can be relabeled first.
    """
    other = aliased(models.IssueLabelAssignment)
    clashing = session.scalars(
        select(models.Issue.number)
        .join(models.IssueLabelAssignment, models.IssueLabelAssignment.issue_id == models.Issue.id)
        .join(other, other.issue_id == models.Issue.id)
        .join(models.IssueLabel, models.IssueLabel.id == other.label_id)
        .where(
            models.IssueLabelAssignment.label_id == label.id,
            models.IssueLabel.id != label.id,
            models.IssueLabel.group_slug == group_slug,
        )
        .distinct()
        .order_by(models.Issue.number)
    ).all()
    if clashing:
        numbers = ', '.join(f'#{number}' for number in clashing)
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'{numbers} already carry another {group_slug!r} label. Relabel them before moving {label.slug!r} into the group.',
        )


@router.get('', response_model=list[schemas.IssueLabel], status_code=status.HTTP_200_OK)
def read_many(session: DbSession):
    """The whole label vocabulary, grouped labels first, with each one's open issue count."""
    return label_views(session)


@router.post('', response_model=schemas.IssueLabelWritten, status_code=status.HTTP_201_CREATED)
def create(label: schemas.IssueLabelCreate, session: DbSession, effects: Effects):
    if session.scalar(select(models.IssueLabel).where(models.IssueLabel.slug == label.slug)) is not None:
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=f'Label {label.slug!r} already exists')
    db_obj = models.IssueLabel(slug=label.slug, group_slug=label.group_slug, description=label.description)
    session.add(db_obj)
    session.commit()
    logger.info('issue_label_created', slug=label.slug, group_slug=label.group_slug)
    return effects.answer(schemas.IssueLabelWritten, label=view(session, db_obj))


@router.get('/{slug}', response_model=schemas.IssueLabel, status_code=status.HTTP_200_OK)
def read_one(label: LabelFromPath, session: DbSession):
    return view(session, label)


@router.patch('/{slug}', response_model=schemas.IssueLabelWritten, status_code=status.HTTP_200_OK)
def update(label: LabelFromPath, update: schemas.IssueLabelUpdate, session: DbSession, effects: Effects):
    update_data = update.model_dump(exclude_unset=True)
    group_slug = update_data.get('group_slug')
    if group_slug is not None and group_slug != label.group_slug:
        ensure_group_holds(session, label, group_slug)
    for attr, value in update_data.items():
        setattr(label, attr, value)
    session.commit()
    return effects.answer(schemas.IssueLabelWritten, label=view(session, label))


@router.delete('/{slug}', response_model=schemas.IssueLabelDeleted, status_code=status.HTTP_200_OK)
def delete(label: LabelFromPath, session: DbSession, effects: Effects):
    """Delete the label and take it off every issue carrying it, naming each one."""
    deleted = view(session, label)
    carriers = session.scalars(
        select(models.Issue.number)
        .join(models.IssueLabelAssignment, models.IssueLabelAssignment.issue_id == models.Issue.id)
        .where(models.IssueLabelAssignment.label_id == label.id)
        .order_by(models.Issue.number)
    ).all()
    for number in carriers:
        effects.did(WriteEffectCode.LABEL_REMOVED, f'#{number} no longer carries {label.slug!r}.', number)
    session.delete(label)
    session.commit()
    logger.info('issue_label_deleted', slug=label.slug, carriers=len(carriers))
    return effects.answer(schemas.IssueLabelDeleted, deleted=deleted)
