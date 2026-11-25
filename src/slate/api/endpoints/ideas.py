"""Ideas: work nobody has taken on, with its priority and refs, until promotion turns it into an initiative or an issue."""

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
from slate.services.idea_promotion import lock_idea
from slate.services.idea_promotion import turn_idea_into
from slate.services.idea_views import idea_detail
from slate.services.idea_views import idea_view
from slate.services.idea_views import idea_views
from slate.services.idea_views import promoted_idea_view
from slate.services.initiative_views import counted
from slate.services.initiatives import new_initiative
from slate.services.issue_readiness import NO_PRIORITY_URGENCY
from slate.services.issue_views import fresh_issue_detail
from slate.services.issues import new_issue
from slate.services.likely_duplicates import warn_likely_duplicates
from slate.services.queue_moves import move_in_queue
from slate.services.record_deletes import name_record_parts_deleted
from slate.services.record_groupings import GroupingsQuery
from slate.services.record_groupings import narrowed_to_groupings
from slate.services.record_lookup import resolve_idea
from slate.services.record_pins import clear_pin_on_close
from slate.services.record_pins import pin_record
from slate.services.record_pins import unpin_record
from slate.services.record_pursuits import pursuits_of
from slate.services.record_pursuits import resolve_pursuits
from slate.services.record_pursuits import set_record_pursuits
from slate.services.record_refs import link_refs
from slate.services.record_summaries import record_summaries
from slate.services.record_tags import resolve_tags
from slate.services.record_tags import set_record_tags
from slate.services.row_limit import RowLimit
from slate.services.row_limit import apply_row_limit
from slate.services.write_effects import Effects
from slate.services.write_effects import WriteEffectCode
from slate.services.write_effects import WriteEffects
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
    """Hold this idea ahead of every priority in the queue, a person's choice alone, until unpinned. A promotion keeps it on the number."""
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


@router.post('/{id}/promote', response_model=schemas.InitiativeWrittenWithIdea | schemas.IssueWritten, status_code=status.HTTP_200_OK)
def promote(
    id: str, request: schemas.IdeaPromote, session: DbSession, zone: RequestZone, caller: CurrentCaller, effects: Effects
) -> schemas.InitiativeWrittenWithIdea | schemas.IssueWritten:
    """Turn the idea into an initiative or an issue under its own number and `id`, in one transaction.

    Every row on the number stays: its comments, documents, refs, tags, checks and waits, and the
    rank, pin and flow on its record. The new kind's row keeps the idea's `created_ts`, so the
    record's age counts from the idea. A field the request leaves out keeps what the record holds.
    The claim and each open worklog end as `promoted`, which closes the shape step, and the answer
    names each. An issue holds no pursuits, so a promotion to one drops the idea's and warns
    `pursuits_dropped` for each. A number another kind holds answers 409, and so does an idea that
    an earlier release promoted into an initiative of its own.
    """
    idea = lock_idea(session, id)
    name_record(idea.number)
    if idea.initiative is not None:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'#{idea.number} was already promoted to initiative #{idea.initiative.number} {idea.initiative.name!r}.',
        )
    now = dt.datetime.now(dt.UTC)
    actor = caller_actor(caller)
    end_with_record(session, idea.number, 'promoted', now, actor, effects)
    if request.kind == 'issue':
        return promote_to_issue(session, idea, request, zone, actor, now, effects)
    return promote_to_initiative(session, idea, request, actor, now, effects)


def kept_unless_sent(request: schemas.IdeaPromote, *fields: str) -> dict[str, object]:
    """Each of `fields` that the request sent. A create sets only the fields it was sent, so the rest keep what the record holds."""
    return {field: getattr(request, field) for field in fields if getattr(request, field) is not None}


def promote_to_initiative(
    session: DbSession, idea: models.Idea, request: schemas.IdeaPromote, actor: str, now: dt.datetime, effects: WriteEffects
) -> schemas.InitiativeWrittenWithIdea:
    promoted = turn_idea_into(session, idea, Kind.INITIATIVE, actor, now)
    initiative = new_initiative(
        session,
        schemas.InitiativeCreate.model_validate(
            {
                'name': request.name or promoted.title,
                'summary': request.summary,
                'description': promoted.description if request.description is None else request.description,
                'status': request.status,
                'priority': promoted.priority if request.priority is None else request.priority,
                'autofix': request.autofix,
                'not_planned_steps': request.not_planned_steps,
                **kept_unless_sent(request, 'tags', 'pursuits'),
            }
        ),
        from_idea=promoted,
    )
    if initiative.status != 'active':
        clear_pin_on_close(initiative, initiative.status, effects)
    session.commit()
    session.refresh(initiative)
    view = counted(session, initiative)
    return effects.answer(schemas.InitiativeWrittenWithIdea, initiative=view, idea=promoted_idea_view(promoted, view, now))


def promote_to_issue(
    session: DbSession,
    idea: models.Idea,
    request: schemas.IdeaPromote,
    zone: str,
    actor: str,
    now: dt.datetime,
    effects: WriteEffects,
) -> schemas.IssueWritten:
    for pursuit in pursuits_of(session, [idea.number]).get(idea.number, []):
        effects.warn(
            WriteEffectCode.PURSUITS_DROPPED,
            f"Dropped pursuit #{pursuit.number} {pursuit.name}, because an issue takes its initiative's pursuits.",
            pursuit.number,
        )
    set_record_pursuits(session, idea.number, [])
    promoted = turn_idea_into(session, idea, Kind.ISSUE, actor, now)
    issue = new_issue(
        session,
        schemas.IssueCreate.model_validate(
            {
                'title': promoted.title,
                'description': promoted.description if request.description is None else request.description,
                'acceptance': request.acceptance,
                'repo': request.repo,
                'paths': request.paths,
                'type': request.type,
                'priority': promoted.priority if request.priority is None else request.priority,
                'initiative': request.initiative,
                'parent': request.parent,
                **kept_unless_sent(request, 'tags'),
            }
        ),
        actor,
        effects,
        from_idea=promoted,
    )
    session.commit()
    return effects.answer(schemas.IssueWritten, issue=fresh_issue_detail(session, issue, zone))
