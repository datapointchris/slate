"""Ideas: work nobody has taken on, with its priority and refs, until promotion makes it an initiative."""

import datetime as dt
from typing import Annotated

import structlog
from fastapi import APIRouter
from fastapi import Depends
from fastapi import HTTPException
from fastapi import status
from sqlalchemy import case
from sqlalchemy import delete as sql_delete
from sqlalchemy import select

from slate import models
from slate import schemas
from slate.api.callers import CurrentCaller
from slate.api.callers import DbSession
from slate.api.callers import caller_actor
from slate.api.callers import signer_named_or_caller
from slate.api.exceptions import Refusal
from slate.api.request_zone import RequestZone
from slate.models.record import Kind
from slate.services.claims import end_with_record
from slate.services.idea_views import idea_detail
from slate.services.idea_views import idea_view
from slate.services.idea_views import idea_views
from slate.services.initiatives import new_initiative
from slate.services.issue_readiness import NO_PRIORITY_URGENCY
from slate.services.likely_duplicates import warn_likely_duplicates
from slate.services.queue_moves import move_in_queue
from slate.services.record_deletes import name_record_parts_deleted
from slate.services.record_groupings import GroupingsQuery
from slate.services.record_groupings import narrowed_to_groupings
from slate.services.record_lookup import resolve_idea
from slate.services.record_pins import carry_pin
from slate.services.record_pins import pin_record
from slate.services.record_pins import unpin_record
from slate.services.record_pursuits import pursuits_of
from slate.services.record_pursuits import resolve_pursuits
from slate.services.record_pursuits import set_record_pursuits
from slate.services.record_refs import carry_refs
from slate.services.record_refs import link_refs
from slate.services.record_summaries import record_summaries
from slate.services.record_tags import own_tags
from slate.services.record_tags import resolve_tags
from slate.services.record_tags import set_record_tags
from slate.services.row_limit import RowLimit
from slate.services.row_limit import apply_row_limit
from slate.services.write_effects import Effects
from slate.services.write_effects import WriteEffectCode
from slate.tracing.named_work import name_record

logger = structlog.get_logger()
router = APIRouter()


def path_idea(id: str, session: DbSession) -> models.Idea:
    idea = resolve_idea(session, id)
    name_record(idea.number)
    return idea


IdeaFromPath = Annotated[models.Idea, Depends(path_idea)]


@router.get('', response_model=list[schemas.Idea], status_code=status.HTTP_200_OK)
def read_many(
    session: DbSession,
    groupings: GroupingsQuery,
    limit: RowLimit = None,
):
    """Every idea, promoted or not, most urgent first. Priority 0 is no priority and sorts last."""
    urgency = case((models.Idea.priority == 0, NO_PRIORITY_URGENCY), else_=models.Idea.priority)
    query = select(models.Idea).order_by(urgency.asc(), models.Idea.number.asc())
    query = narrowed_to_groupings(query, Kind.IDEA, groupings, session)
    return idea_views(session, session.scalars(apply_row_limit(query, limit)).all())


@router.post('', response_model=schemas.IdeaWritten, status_code=status.HTTP_201_CREATED)
def create(idea: schemas.IdeaCreate, session: DbSession, caller: CurrentCaller, effects: Effects):
    """Record an idea last in the queue with its refs, warning about each open record whose title reads like it."""
    tags = resolve_tags(session, idea.tags)
    pursuits = resolve_pursuits(session, idea.pursuits)
    db_obj = models.Idea(title=idea.title, description=idea.description, priority=idea.priority)
    session.add(db_obj)
    session.flush()
    name_record(db_obj.number)
    set_record_tags(session, db_obj.number, tags)
    set_record_pursuits(session, db_obj.number, pursuits)
    link_refs(session, db_obj.number, idea.refs, None, caller_actor(caller), effects)
    warn_likely_duplicates(session, db_obj.number, db_obj.title, None, effects)
    session.commit()
    session.refresh(db_obj)
    return effects.answer(schemas.IdeaWritten, idea=idea_view(session, db_obj))


@router.post('/{id}/rank', response_model=schemas.IdeaWritten, status_code=status.HTTP_200_OK)
def rank(idea: IdeaFromPath, move: schemas.RecordRankMove, session: DbSession, zone: RequestZone, effects: Effects):
    """Place this idea directly before or after another record of its priority in the queue."""
    move_in_queue(session, zone, idea.record, move)
    session.commit()
    session.refresh(idea)
    return effects.answer(schemas.IdeaWritten, idea=idea_view(session, idea))


@router.post('/{id}/pin', response_model=schemas.IdeaWritten, status_code=status.HTTP_200_OK)
def pin(idea: IdeaFromPath, request: schemas.RecordPin, session: DbSession, caller: CurrentCaller, effects: Effects):
    """Hold this idea ahead of every priority in the queue, a person's choice alone, until unpinned or promoted."""
    if idea.initiative is not None:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'#{idea.number} was promoted to initiative #{idea.initiative.number}, so pin that instead.',
        )
    pin_record(session, idea.record, request, caller_actor(caller), effects)
    session.commit()
    session.refresh(idea)
    return effects.answer(schemas.IdeaWritten, idea=idea_view(session, idea))


@router.delete('/{id}/pin', response_model=schemas.IdeaWritten, status_code=status.HTTP_200_OK)
def unpin(idea: IdeaFromPath, session: DbSession, effects: Effects):
    """Return this idea to the place its priority and rank give it."""
    unpin_record(idea.record, effects)
    session.commit()
    session.refresh(idea)
    return effects.answer(schemas.IdeaWritten, idea=idea_view(session, idea))


@router.get('/{id}', response_model=schemas.IdeaDetail, status_code=status.HTTP_200_OK)
def read_one(idea: IdeaFromPath, session: DbSession):
    return idea_detail(session, idea)


@router.patch('/{id}', response_model=schemas.IdeaWritten, status_code=status.HTTP_200_OK)
def update(idea: IdeaFromPath, changes: schemas.IdeaUpdate, session: DbSession, effects: Effects):
    update_data = changes.model_dump(exclude_unset=True)
    if 'tags' in update_data:
        set_record_tags(session, idea.number, resolve_tags(session, update_data.pop('tags')))
    if 'pursuits' in update_data:
        set_record_pursuits(session, idea.number, resolve_pursuits(session, update_data.pop('pursuits')))
    for field, value in update_data.items():
        setattr(idea, field, value)
    idea.updated_ts = dt.datetime.now(dt.UTC)
    session.commit()
    session.refresh(idea)
    return effects.answer(schemas.IdeaWritten, idea=idea_view(session, idea))


@router.delete('/{id}', response_model=schemas.RecordDeleted, status_code=status.HTTP_200_OK)
def delete(idea: IdeaFromPath, session: DbSession, caller: CurrentCaller, effects: Effects):
    """Delete the idea's record, which takes the idea, its refs, pin, claims, worklogs, documents and comments with it.

    A promoted idea is refused with 409, because its initiative names it as where that work came from.
    """
    if idea.initiative is not None:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'#{idea.number} was promoted to initiative #{idea.initiative.number} {idea.initiative.name!r}, '
            'which names it as where that work came from, so it stays.',
        )
    deleted = record_summaries(session, [idea.number])[idea.number]
    name_record_parts_deleted(session, idea.number, caller_actor(caller), dt.datetime.now(dt.UTC), effects)
    session.execute(sql_delete(models.Record).where(models.Record.number == idea.number))
    session.commit()
    logger.info('idea_deleted', number=deleted.number)
    return effects.answer(schemas.RecordDeleted, deleted=deleted)


@router.post('/{id}/refs', response_model=schemas.IdeaWritten, status_code=status.HTTP_200_OK)
def link(idea: IdeaFromPath, request: schemas.RecordRefsLink, session: DbSession, caller: CurrentCaller, effects: Effects):
    """Link each ref. A ref already linked keeps its note and linker, and the answer warns."""
    linker = signer_named_or_caller(caller, request.linked_by, Refusal.SCOPED_CLIENT_LINKS_REFS_AS_ITSELF)
    link_refs(session, idea.number, request.refs, request.note, linker, effects)
    idea.updated_ts = dt.datetime.now(dt.UTC)
    session.commit()
    session.refresh(idea)
    return effects.answer(schemas.IdeaWritten, idea=idea_view(session, idea))


@router.post('/{id}/evidence', response_model=schemas.IdeaWritten, status_code=status.HTTP_200_OK, deprecated=True)
def link_evidence(idea: IdeaFromPath, request: schemas.RecordRefsLink, session: DbSession, caller: CurrentCaller, effects: Effects):
    """`POST /ideas/{id}/refs`, under the path that `slate ideas link` sent before refs."""
    return link(idea, request, session, caller, effects)


@router.post('/{id}/promote', response_model=schemas.IdeaWritten, status_code=status.HTTP_200_OK)
def promote(idea: IdeaFromPath, request: schemas.IdeaPromote, session: DbSession, caller: CurrentCaller, effects: Effects):
    """Create the initiative the idea becomes, naming the idea. A promoted idea is refused with 409.

    The summary is required, because an idea has none. Each other field the request leaves out
    takes the idea's own: its title as the name, its description, its priority, its tags, its pursuits.
    The initiative carries each of the idea's refs as it was linked, and the idea keeps them too.
    An idea marks no step it will not run, so the initiative plans every step but those the request
    names. The answer is the idea, which then shows the initiative. The idea's claim ends as `promoted`,
    because its work is the initiative's now.
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
            summary=request.summary,
            description=request.description if request.description is not None else idea.description,
            status=request.status,
            priority=request.priority if request.priority is not None else idea.priority,
            autofix=request.autofix,
            tags=request.tags if request.tags is not None else own_tags(session, [idea.number]).get(idea.number, []),
            pursuits=request.pursuits
            if request.pursuits is not None
            else [pursuit.number for pursuit in pursuits_of(session, [idea.number]).get(idea.number, [])],
            not_planned_steps=request.not_planned_steps,
        ),
        idea=idea,
    )
    effects.did(
        WriteEffectCode.INITIATIVE_CREATED,
        f'Created initiative #{initiative.number} {initiative.name!r} from #{idea.number}.',
        initiative.number,
    )
    carry_pin(idea, initiative, effects)
    carry_refs(session, idea.number, initiative.number, effects)
    now = dt.datetime.now(dt.UTC)
    end_with_record(session, idea.number, 'promoted', now, caller_actor(caller), effects)
    idea.updated_ts = now
    session.commit()
    session.refresh(idea)
    return effects.answer(schemas.IdeaWritten, idea=idea_view(session, idea))
