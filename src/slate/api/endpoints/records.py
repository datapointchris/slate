"""Any record by its number, whatever kind holds it, and what an issue or an initiative waits on."""

from typing import Annotated

from fastapi import APIRouter
from fastapi import Depends
from fastapi import Query
from fastapi import status
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.api.callers import DbSession
from slate.api.request_zone import RequestZone
from slate.models.record import Kind
from slate.schemas.record_read import DirectionRecord
from slate.schemas.record_read import IdeaRecord
from slate.schemas.record_read import InitiativeRecord
from slate.schemas.record_read import IssueRecord
from slate.schemas.record_read import RecordRead
from slate.services.directions import direction_detail
from slate.services.idea_views import IDEA_LOAD_OPTIONS
from slate.services.idea_views import idea_detail
from slate.services.initiative_views import counted
from slate.services.initiative_views import initiative_detail
from slate.services.issue_views import fresh_issue_detail
from slate.services.record_changes import record_changes
from slate.services.record_dependencies import add_initiative_wait
from slate.services.record_dependencies import add_issue_dependency
from slate.services.record_dependencies import remove_initiative_wait
from slate.services.record_dependencies import remove_issue_dependency
from slate.services.record_lists import listed_records
from slate.services.record_refs import resolve_issue_or_initiative
from slate.services.record_refs import resolve_record
from slate.services.row_limit import RowLimit
from slate.services.write_effects import Effects
from slate.services.write_effects import WriteEffects
from slate.tracing.named_work import name_record

router = APIRouter()


@router.get('', response_model=list[schemas.RecordListed], status_code=status.HTTP_200_OK)
def read_many(
    session: DbSession,
    search: Annotated[
        str | None,
        Query(description='Words that must each appear in the title or description, in any order. `N` or `#N` also matches record N'),
    ] = None,
    kind: Annotated[Kind | None, Query(description='Only records of this kind')] = None,
    limit: RowLimit = None,
) -> list[schemas.RecordListed]:
    """Records of every kind and every status: a record a word names by number first, then the unclosed, the most urgent and the newest.

    No status is left out, because the record a reader remembers may be the one that closed. An
    idea has no status, so it lists among the unclosed.
    """
    return listed_records(session, search, list(Kind) if kind is None else [kind], limit)


@router.get('/{number}', response_model=RecordRead, status_code=status.HTTP_200_OK)
def read_one(number: str, session: DbSession, zone: RequestZone) -> RecordRead:
    """The record holding `number`, as `N` or `#N`, with `kind` saying which read `record` is.

    Each kind's read is the one its own route answers, with the record's whole claim history.
    """
    record = resolve_record(session, number)
    name_record(record.number)
    match record.kind:
        case Kind.ISSUE:
            issue = session.scalars(select(models.Issue).where(models.Issue.number == record.number)).one()
            return IssueRecord(number=record.number, record=fresh_issue_detail(session, issue, zone))
        case Kind.INITIATIVE:
            initiative = session.scalars(select(models.Initiative).where(models.Initiative.number == record.number)).one()
            return InitiativeRecord(number=record.number, record=initiative_detail(session, initiative))
        case Kind.IDEA:
            idea = session.scalars(select(models.Idea).options(*IDEA_LOAD_OPTIONS).where(models.Idea.number == record.number)).one()
            return IdeaRecord(number=record.number, record=idea_detail(session, idea))
        case Kind.DIRECTION:
            direction = session.scalars(select(models.Direction).where(models.Direction.number == record.number)).one()
            return DirectionRecord(number=record.number, record=direction_detail(session, direction))


@router.get('/{number}/changes', response_model=list[schemas.RecordChange], status_code=status.HTTP_200_OK)
def read_changes(number: str, session: DbSession, limit: RowLimit = None) -> list[schemas.RecordChange]:
    """Every write to the record and to each row hanging off it, newest first, so `limit` keeps the latest."""
    record = resolve_record(session, number)
    name_record(record.number)
    return record_changes(session, record, limit)


def path_waiter(ref: str, session: DbSession) -> models.Issue | models.Initiative:
    """Resolve the `{ref}` segment: an issue or an initiative by number or UUID, or an initiative by name."""
    waiter = resolve_issue_or_initiative(session, ref)
    name_record(waiter.number)
    return waiter


WaiterFromPath = Annotated[models.Issue | models.Initiative, Depends(path_waiter)]
WaiterWritten = schemas.IssueWritten | schemas.InitiativeWritten


def waiter_written(session: Session, waiter: models.Issue | models.Initiative, zone: str, effects: WriteEffects) -> WaiterWritten:
    """Commit, then answer the waiter as its own kind's routes do."""
    session.commit()
    if isinstance(waiter, models.Issue):
        return effects.answer(schemas.IssueWritten, issue=fresh_issue_detail(session, waiter, zone))
    session.refresh(waiter)
    return effects.answer(schemas.InitiativeWritten, initiative=counted(session, waiter))


@router.post('/{ref:path}/dependencies', response_model=WaiterWritten, status_code=status.HTTP_201_CREATED)
def add_dependency(
    waiter: WaiterFromPath, dependency: schemas.RecordDependencyCreate, session: DbSession, zone: RequestZone, effects: Effects
) -> WaiterWritten:
    """Record that the issue or initiative `ref` names waits on another record.

    An issue waits on an issue it depends on. An initiative waits on an initiative or an issue, and
    every unclosed issue in it is blocked while that record is open. An edge that would leave a
    record waiting on itself, through any mix of dependencies, parents and initiatives' waits, is
    refused with the loop it closes.
    """
    if isinstance(waiter, models.Issue):
        add_issue_dependency(session, waiter, dependency.depends_on)
    else:
        add_initiative_wait(session, waiter, dependency.depends_on)
    return waiter_written(session, waiter, zone, effects)


@router.delete('/{ref:path}/dependencies/{dep_id}', response_model=WaiterWritten, status_code=status.HTTP_200_OK)
def remove_dependency(waiter: WaiterFromPath, dep_id: str, session: DbSession, zone: RequestZone, effects: Effects) -> WaiterWritten:
    """Remove the edge, answering the waiter with what it still waits on."""
    if isinstance(waiter, models.Issue):
        remove_issue_dependency(session, waiter, dep_id)
    else:
        remove_initiative_wait(session, waiter, dep_id)
    return waiter_written(session, waiter, zone, effects)
