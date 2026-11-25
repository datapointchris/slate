"""Ideas: work nobody has taken on, with its priority and refs.

A promotion turns an idea into an initiative or an issue, and a fold ends it into the work that covers it.
"""

import datetime as dt
from typing import Annotated

import structlog
from fastapi import APIRouter
from fastapi import Depends
from fastapi import HTTPException
from fastapi import status
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
from slate.models.record_redirect import FOLDED
from slate.schemas.idea import FIELDS_OF_KIND
from slate.schemas.idea import SHARED_FIELDS
from slate.schemas.idea import PromotedKind
from slate.services.claims import end_with_record
from slate.services.idea_folds import fold_rows_into
from slate.services.idea_folds import lock_fold
from slate.services.idea_folds import name_folded_rows
from slate.services.idea_folds import settle_claims
from slate.services.idea_promotion import PromotedIdea
from slate.services.idea_promotion import idea_held_for_write
from slate.services.idea_promotion import lock_idea
from slate.services.idea_promotion import refuse_named_by_initiative
from slate.services.idea_promotion import turn_idea_into
from slate.services.idea_views import idea_detail
from slate.services.idea_views import idea_view
from slate.services.idea_views import listed_ideas
from slate.services.idea_views import promoted_idea_view
from slate.services.initiative_views import counted
from slate.services.initiatives import new_initiative
from slate.services.issue_views import fresh_issue_detail
from slate.services.issues import new_issue
from slate.services.likely_duplicates import warn_likely_duplicates
from slate.services.queue_moves import move_in_queue
from slate.services.record_deletes import name_record_parts_deleted
from slate.services.record_groupings import GroupingsQuery
from slate.services.record_lookup import REQUEST_WRITES
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
from slate.services.write_effects import Effects
from slate.services.write_effects import WriteEffectCode
from slate.services.write_effects import WriteEffects
from slate.tracing.named_work import name_record

logger = structlog.get_logger()
router = APIRouter()


def path_idea(id: str, session: DbSession) -> models.Idea:
    """The idea the path names. A write holds its `records` row, so it waits behind a fold or a promotion and meets what that left."""
    idea = resolve_idea(session, id)
    if session.info.get(REQUEST_WRITES):
        idea = idea_held_for_write(session, id, idea.number)
    name_record(idea.number)
    return idea


IdeaFromPath = Annotated[models.Idea, Depends(path_idea)]


@router.get('', response_model=list[schemas.Idea], status_code=status.HTTP_200_OK)
def read_many(
    session: DbSession,
    groupings: GroupingsQuery,
    limit: RowLimit = None,
):
    """Every idea, promoted or not, most urgent first. Priority 0 is no priority and sorts last.

    Every number that is or was an idea is listed once. `became` is null for an open idea, and
    otherwise says how it ended and names the record that carries its work, as that record stands.
    A promoted record reads as the idea it was. A number a fold retired reads from its redirect,
    and so does an idea promoted before promotion kept the number. A tag, a pursuit or a ref leaves
    a retired number out, because its rows moved to the record it leads to.
    """
    return listed_ideas(session, groupings, limit)


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
    refuse_named_by_initiative(idea)
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
    an initiative names in `idea_id`.
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


def create_fields(request: schemas.IdeaPromote, kind: PromotedKind, promoted: PromotedIdea) -> dict[str, object]:
    """The fields of `kind`'s create: each the request sent, else the idea's description and priority.

    A field left unsent stays out, so the create's default applies, and its tags and pursuits keep what the record holds.
    """
    sent = request.model_dump(include=set(FIELDS_OF_KIND[kind] | SHARED_FIELDS), exclude_unset=True, exclude_none=True)
    return {'description': promoted.description, 'priority': promoted.priority, **sent}


def promote_to_initiative(
    session: DbSession, idea: models.Idea, request: schemas.IdeaPromote, actor: str, now: dt.datetime, effects: WriteEffects
) -> schemas.InitiativeWrittenWithIdea:
    promoted = turn_idea_into(session, idea, Kind.INITIATIVE, actor, now)
    initiative = new_initiative(
        session,
        schemas.InitiativeCreate.model_validate({'name': promoted.title, **create_fields(request, 'initiative', promoted)}),
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
        schemas.IssueCreate.model_validate({'title': promoted.title, **create_fields(request, 'issue', promoted)}),
        actor,
        effects,
        from_idea=promoted,
    )
    session.commit()
    return effects.answer(schemas.IssueWritten, issue=fresh_issue_detail(session, issue, zone))


FoldWritten = schemas.IdeaWritten | schemas.InitiativeWritten | schemas.IssueWritten


@router.post('/{id}/fold', response_model=FoldWritten, status_code=status.HTTP_200_OK)
def fold(id: str, request: schemas.IdeaFold, session: DbSession, zone: RequestZone, caller: CurrentCaller, effects: Effects) -> FoldWritten:
    """Fold the idea into the idea, initiative or issue `into` names, which covers its work, in one transaction.

    Every row on the idea's number moves to the target: worklogs, renumbered after the target's
    last, claims and their waits, comments, checks, documents with their line comments, and refs,
    tags and pursuits as a union. A document whose name the target holds moves as
    `<name>-from-<idea>`. The idea's number, its `id` and each worklog's old name then lead there,
    and so does every number that led to the idea. The target keeps its number, its fields and its
    place in the queue, so the idea's pin is dropped with a warning. A closed target is accepted,
    because the idea's work may already have shipped there.

    Another session's live claim on either record answers 409 naming its holder. `holder`'s claim
    on the idea moves to the target, or ends as released where `holder` holds the target too. Left
    out, `holder` is the caller. A non-idea, a pursuit, the idea itself, an idea that an initiative
    names in `idea_id` and a number a fold already retired each answer 409. The answer is the
    target's own kind's envelope, naming each row moved in `side_effects`.
    """
    idea, target = lock_fold(session, id, request.into)
    idea_number, target_number = idea.number, target.number
    name_record(idea_number)
    now = dt.datetime.now(dt.UTC)
    actor = caller_actor(caller)
    settle_claims(session, idea_number, target_number, request.holder or actor, now, effects)
    folded = fold_rows_into(session, idea_number, target_number, reason=FOLDED, redirected_ts=now, redirected_by=actor)
    name_folded_rows(folded, effects)
    session.commit()
    return fold_target_written(session, target_number, zone, effects)


def fold_target_written(session: DbSession, number: int, zone: str, effects: WriteEffects) -> FoldWritten:
    """The fold's target, answered in its own kind's envelope."""
    kind = session.scalar(select(models.Record.kind).where(models.Record.number == number))
    match kind:
        case Kind.IDEA:
            target_idea = session.scalars(select(models.Idea).where(models.Idea.number == number)).one()
            return effects.answer(schemas.IdeaWritten, idea=idea_view(session, target_idea))
        case Kind.INITIATIVE:
            initiative = session.scalars(select(models.Initiative).where(models.Initiative.number == number)).one()
            return effects.answer(schemas.InitiativeWritten, initiative=counted(session, initiative))
        case _:
            issue = session.scalars(select(models.Issue).where(models.Issue.number == number)).one()
            return effects.answer(schemas.IssueWritten, issue=fresh_issue_detail(session, issue, zone))
