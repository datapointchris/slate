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

from slate import schemas
from slate.api.callers import CurrentCaller
from slate.api.callers import DbSession
from slate.api.callers import caller_actor
from slate.models.document import DOCUMENT_NAME_PATTERN
from slate.services.document_views import document_view
from slate.services.document_views import list_documents
from slate.services.documents import find_document
from slate.services.documents import resolve_document
from slate.services.documents import resolve_version
from slate.services.documents import write_version
from slate.services.record_lookup import redirect_named
from slate.services.record_lookup import resolve_record
from slate.services.row_limit import RECORD_LIMIT_DEFAULT
from slate.services.row_limit import RecordLimit
from slate.services.row_limit import RowLimit
from slate.services.write_effects import Effects
from slate.tracing.named_work import name_agent_session
from slate.tracing.named_work import name_record

router = APIRouter()

TOTAL_RECORDS_HEADER = 'X-Total-Records'

DocumentName = Annotated[str, Path(pattern=DOCUMENT_NAME_PATTERN, description='Lowercase words joined by hyphens, unique on its record')]


@router.get(
    '',
    response_model=list[schemas.ListedDocument],
    status_code=status.HTTP_200_OK,
    responses={
        status.HTTP_200_OK: {
            'headers': {
                TOTAL_RECORDS_HEADER: {
                    'description': 'How many records hold a matching document, so a client tells a short page from the whole',
                    'schema': {'type': 'integer'},
                }
            }
        }
    },
)
def read_many(
    session: DbSession,
    response: Response,
    record: Annotated[str | None, Query(description='Only the documents on this record, by number')] = None,
    record_limit: RecordLimit = RECORD_LIMIT_DEFAULT,
    limit: RowLimit = None,
):
    """Every document on the newest `record_limit` records, each with its record and latest version, the most recently written first.

    A record ranks by its latest document write, so the bound never splits a record's documents.
    `limit` then cuts rows.
    """
    number = None
    if record is not None:
        number = resolve_record(session, record).number
        name_record(number)
    documents, total = list_documents(session, number, record_limit, limit)
    response.headers[TOTAL_RECORDS_HEADER] = str(total)
    return documents


@router.get('/{record}/{name}', response_model=schemas.Document, status_code=status.HTTP_200_OK)
def read_one(
    record: str,
    name: DocumentName,
    session: DbSession,
    version: Annotated[int | None, Query(ge=1, description='The version to read, else the latest')] = None,
):
    """One version of the document, with every version listed and every line comment placed on this one.

    A line comment written on another version sits on the line its own became here, or on no line
    when this version changed or removed it. Through a number a fold retired, it answers the
    document the fold renamed `<name>-from-<number>`, and the target's own `<name>` where none was.
    """
    number = resolve_record(session, record).number
    name_record(number)
    redirect = redirect_named(session, record)
    if redirect is not None and find_document(session, number, renamed := f'{name}-from-{redirect.number}') is not None:
        name = renamed
    document = resolve_document(session, number, name)
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
    name_record(number)
    name_agent_session(request.session)
    document, created = write_version(session, number, name, request, caller_actor(caller), dt.datetime.now(dt.UTC), effects)
    session.commit()
    if created:
        response.status_code = status.HTTP_201_CREATED
    return effects.answer(schemas.DocumentWritten, document=document_view(session, document, document.versions[-1]))
