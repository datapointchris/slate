"""Documents on any record, written a version at a time and read with every line comment placed on the version read.

A document is addressed by its record and its name, as `/documents/1811/plan`. A write is a PUT of
the next version, the way an object store with versioning keeps every PUT: S3 answers a read with
the latest and keeps each earlier version readable by its id.
"""

import datetime as dt
from typing import Annotated

from fastapi import APIRouter
from fastapi import Path
from fastapi import Query
from fastapi import Response
from fastapi import status
from sqlalchemy import func
from sqlalchemy import select

from slate import models
from slate import schemas
from slate.api.callers import CurrentCaller
from slate.api.callers import DbSession
from slate.api.callers import caller_actor
from slate.models.document import DOCUMENT_NAME_PATTERN
from slate.services.document_views import DOCUMENT_LOAD_OPTIONS
from slate.services.document_views import document_summaries
from slate.services.document_views import document_view
from slate.services.documents import resolve_document
from slate.services.documents import resolve_version
from slate.services.documents import write_version
from slate.services.record_refs import resolve_record
from slate.services.row_limit import RowLimit
from slate.services.row_limit import apply_row_limit
from slate.services.write_effects import Effects

router = APIRouter()

DocumentName = Annotated[str, Path(pattern=DOCUMENT_NAME_PATTERN, description='Lowercase words joined by hyphens, unique on its record')]


@router.get('', response_model=list[schemas.DocumentSummary], status_code=status.HTTP_200_OK)
def read_many(
    session: DbSession,
    record: Annotated[str | None, Query(description='Only the documents on this record, by number')] = None,
    limit: RowLimit = None,
):
    """Documents with their latest version, the most recently written first."""
    latest = (
        select(models.DocumentVersion.document_id, func.max(models.DocumentVersion.written_ts).label('written_ts'))
        .group_by(models.DocumentVersion.document_id)
        .subquery()
    )
    query = (
        select(models.Document)
        .options(*DOCUMENT_LOAD_OPTIONS)
        .join(latest, latest.c.document_id == models.Document.id)
        .order_by(latest.c.written_ts.desc(), models.Document.id.desc())
    )
    if record is not None:
        query = query.where(models.Document.record_number == resolve_record(session, record).number)
    return document_summaries(session, session.scalars(apply_row_limit(query, limit)).all())


@router.get('/{record}/{name}', response_model=schemas.Document, status_code=status.HTTP_200_OK)
def read_one(
    record: str,
    name: DocumentName,
    session: DbSession,
    version: Annotated[int | None, Query(ge=1, description='The version to read, else the latest')] = None,
):
    """One version of the document, with every version listed and every line comment placed on this one.

    A line comment written on another version sits on the line its own became here, or on no line
    when this version changed or removed it.
    """
    document = resolve_document(session, resolve_record(session, record).number, name)
    return document_view(session, document, resolve_version(document, version))


@router.put('/{record}/{name}', response_model=schemas.DocumentWritten, status_code=status.HTTP_200_OK)
def write(
    record: str,
    name: DocumentName,
    request: schemas.DocumentWrite,
    response: Response,
    session: DbSession,
    caller: CurrentCaller,
    effects: Effects,
):
    """Write the document's next version, creating the document with 201 on its first.

    The version names the caller, the agent session writing it, and the worklog that session is in.
    A session in no live worklog is a warning. A body matching the latest version writes nothing and
    warns. Naming a kind other than the document's is a 409.
    """
    number = resolve_record(session, record).number
    document, created = write_version(session, number, name, request, caller_actor(caller), dt.datetime.now(dt.UTC), effects)
    session.commit()
    if created:
        response.status_code = status.HTTP_201_CREATED
    return effects.answer(schemas.DocumentWritten, document=document_view(session, document, document.versions[-1]))
