"""Ideas: work nobody has taken on, with its priority and evidence, until promotion makes it an initiative."""

import datetime as dt
from typing import Annotated

import structlog
from fastapi import APIRouter
from fastapi import Depends
from fastapi import HTTPException
from fastapi import status
from sqlalchemy import case
from sqlalchemy import select

from slate import models
from slate import schemas
from slate.api.callers import DbSession
from slate.services.idea_views import IDEA_LOAD_OPTIONS
from slate.services.idea_views import idea_detail
from slate.services.idea_views import idea_view
from slate.services.idea_views import idea_views
from slate.services.initiatives import new_initiative
from slate.services.issue_readiness import NO_PRIORITY_URGENCY
from slate.services.record_refs import resolve_idea
from slate.services.row_limit import RowLimit
from slate.services.row_limit import apply_row_limit
from slate.services.write_effects import Effects
from slate.services.write_effects import WriteEffectCode

logger = structlog.get_logger()
router = APIRouter()


def path_idea(id: str, session: DbSession) -> models.Idea:
    return resolve_idea(session, id)


IdeaFromPath = Annotated[models.Idea, Depends(path_idea)]


@router.get('', response_model=list[schemas.Idea], status_code=status.HTTP_200_OK)
def read_many(session: DbSession, limit: RowLimit = None):
    """Every idea, promoted or not, most urgent first. Priority 0 is no priority and sorts last."""
    urgency = case((models.Idea.priority == 0, NO_PRIORITY_URGENCY), else_=models.Idea.priority)
    query = select(models.Idea).options(*IDEA_LOAD_OPTIONS).order_by(urgency.asc(), models.Idea.number.asc())
    return idea_views(session, session.scalars(apply_row_limit(query, limit)).all())


@router.post('', response_model=schemas.IdeaWritten, status_code=status.HTTP_201_CREATED)
def create(idea: schemas.IdeaCreate, session: DbSession, effects: Effects):
    db_obj = models.Idea(title=idea.title, description=idea.description, priority=idea.priority)
    session.add(db_obj)
    session.commit()
    session.refresh(db_obj)
    return effects.answer(schemas.IdeaWritten, idea=idea_view(session, db_obj))


@router.get('/{id}', response_model=schemas.IdeaDetail, status_code=status.HTTP_200_OK)
def read_one(idea: IdeaFromPath, session: DbSession):
    return idea_detail(session, idea)


@router.patch('/{id}', response_model=schemas.IdeaWritten, status_code=status.HTTP_200_OK)
def update(idea: IdeaFromPath, changes: schemas.IdeaUpdate, session: DbSession, effects: Effects):
    for field, value in changes.model_dump(exclude_unset=True).items():
        setattr(idea, field, value)
    idea.updated_ts = dt.datetime.now(dt.UTC)
    session.commit()
    session.refresh(idea)
    return effects.answer(schemas.IdeaWritten, idea=idea_view(session, idea))


@router.post('/{id}/evidence', response_model=schemas.IdeaWritten, status_code=status.HTTP_200_OK)
def link_evidence(idea: IdeaFromPath, link: schemas.IdeaEvidenceLink, session: DbSession, effects: Effects):
    """Link each ref as evidence. A ref already linked keeps its note and linker, and the answer warns."""
    linked = {evidence.ref for evidence in idea.evidence}
    for ref in dict.fromkeys(link.refs):
        if ref in linked:
            effects.warn(
                WriteEffectCode.EVIDENCE_ALREADY_LINKED, f'#{idea.number} already links {ref!r}, so it is left as it was.', idea.number
            )
            continue
        idea.evidence.append(models.IdeaEvidence(ref=ref, note=link.note, linked_by=link.linked_by))
    idea.updated_ts = dt.datetime.now(dt.UTC)
    session.commit()
    session.refresh(idea)
    return effects.answer(schemas.IdeaWritten, idea=idea_view(session, idea))


@router.post('/{id}/promote', response_model=schemas.IdeaWritten, status_code=status.HTTP_200_OK)
def promote(idea: IdeaFromPath, request: schemas.IdeaPromote, session: DbSession, effects: Effects):
    """Create the initiative the idea becomes, naming the idea. A promoted idea is refused with 409.

    Each field the request leaves out takes the idea's own: its title as the name, its
    description, its priority. The answer is the idea, which then shows the initiative.
    """
    if idea.initiative is not None:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'#{idea.number} was already promoted to initiative #{idea.initiative.number} {idea.initiative.name!r}.',
        )
    initiative = new_initiative(
        session,
        schemas.InitiativeCreate(
            name=request.name or idea.title,
            description=request.description if request.description is not None else idea.description,
            status=request.status,
            priority=request.priority if request.priority is not None else idea.priority,
            autofix=request.autofix,
        ),
        idea=idea,
    )
    effects.did(
        WriteEffectCode.INITIATIVE_CREATED,
        f'Created initiative #{initiative.number} {initiative.name!r} from #{idea.number}.',
        initiative.number,
    )
    idea.updated_ts = dt.datetime.now(dt.UTC)
    session.commit()
    session.refresh(idea)
    return effects.answer(schemas.IdeaWritten, idea=idea_view(session, idea))
