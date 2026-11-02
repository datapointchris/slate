"""Any record by its number, whatever kind holds it."""

import datetime as dt

from fastapi import APIRouter
from fastapi import status
from sqlalchemy import select

from slate import models
from slate import schemas
from slate.api.callers import DbSession
from slate.api.endpoints.issues import fresh_issue_detail
from slate.api.request_zone import RequestZone
from slate.schemas.record_read import IdeaRecord
from slate.schemas.record_read import InitiativeRecord
from slate.schemas.record_read import IssueRecord
from slate.schemas.record_read import RecordRead
from slate.services.claim_views import claim_history
from slate.services.idea_views import IDEA_LOAD_OPTIONS
from slate.services.idea_views import idea_detail
from slate.services.initiative_views import counted
from slate.services.record_refs import resolve_record

router = APIRouter()


@router.get('/{number}', response_model=RecordRead, status_code=status.HTTP_200_OK)
def read_one(number: str, session: DbSession, zone: RequestZone) -> RecordRead:
    """The record holding `number`, as `N` or `#N`, with `kind` saying which read `record` is.

    Each kind's read is the one its own route answers, with the record's whole claim history.
    """
    record = resolve_record(session, number)
    if record.kind == models.Issue.RECORD_KIND:
        issue = session.scalars(select(models.Issue).where(models.Issue.number == record.number)).one()
        return IssueRecord(number=record.number, record=fresh_issue_detail(session, issue, zone))
    if record.kind == models.Initiative.RECORD_KIND:
        initiative = session.scalars(select(models.Initiative).where(models.Initiative.number == record.number)).one()
        claims = claim_history(session, record.number, dt.datetime.now(dt.UTC))
        detail = schemas.InitiativeDetail(**counted(session, initiative).model_dump(), claims=claims)
        return InitiativeRecord(number=record.number, record=detail)
    idea = session.scalars(select(models.Idea).options(*IDEA_LOAD_OPTIONS).where(models.Idea.number == record.number)).one()
    return IdeaRecord(number=record.number, record=idea_detail(session, idea))
