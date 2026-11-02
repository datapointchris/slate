"""Any record by its number, whatever kind holds it."""

from typing import Annotated

from fastapi import APIRouter
from fastapi import Query
from fastapi import status
from sqlalchemy import select

from slate import models
from slate import schemas
from slate.api.callers import DbSession
from slate.api.request_zone import RequestZone
from slate.models.record import Kind
from slate.schemas.record_read import IdeaRecord
from slate.schemas.record_read import InitiativeRecord
from slate.schemas.record_read import IssueRecord
from slate.schemas.record_read import RecordRead
from slate.services.idea_views import IDEA_LOAD_OPTIONS
from slate.services.idea_views import idea_detail
from slate.services.initiative_views import initiative_detail
from slate.services.issue_views import fresh_issue_detail
from slate.services.record_changes import record_changes
from slate.services.record_lists import listed_records
from slate.services.record_refs import resolve_record
from slate.services.row_limit import RowLimit
from slate.tracing.named_work import name_record

router = APIRouter()


@router.get('', response_model=list[schemas.RecordListed], status_code=status.HTTP_200_OK)
def read_many(
    session: DbSession,
    search: Annotated[str | None, Query(description='Words that must each appear in the title or description, in any order')] = None,
    kind: Annotated[Kind | None, Query(description='Only records of this kind')] = None,
    limit: RowLimit = None,
) -> list[schemas.RecordListed]:
    """Records of every kind and every status, unclosed first, then the most urgent, then the newest.

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


@router.get('/{number}/changes', response_model=list[schemas.RecordChange], status_code=status.HTTP_200_OK)
def read_changes(number: str, session: DbSession, limit: RowLimit = None) -> list[schemas.RecordChange]:
    """Every write to the record and to each row hanging off it, newest first, so `limit` keeps the latest."""
    record = resolve_record(session, number)
    name_record(record.number)
    return record_changes(session, record, limit)
