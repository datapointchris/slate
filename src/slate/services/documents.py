"""Write a document's next version, and find a document or one of its versions by name.

A version names the worklog its session is in when it is written, read through `current_worklog`,
which is the one a commit made at the same moment would be stamped with.
"""

from __future__ import annotations

import datetime as dt

import structlog
from fastapi import HTTPException
from fastapi import status
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.api.exceptions import NotFoundException
from slate.services.worklog_views import current_worklog
from slate.services.worklogs import record_session
from slate.services.write_effects import WriteEffectCode
from slate.services.write_effects import WriteEffects

logger = structlog.get_logger()


def find_document(session: Session, number: int, name: str) -> models.Document | None:
    return session.scalar(select(models.Document).where(models.Document.record_number == number, models.Document.name == name))


def resolve_document(session: Session, number: int, name: str) -> models.Document:
    """The record's document named `name`, or 404 naming the documents the record holds."""
    document = find_document(session, number, name)
    if document is not None:
        return document
    held = session.scalars(select(models.Document.name).where(models.Document.record_number == number).order_by(models.Document.name)).all()
    if held:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail=f'#{number} has no document {name!r}; it holds {", ".join(held)}')
    raise NotFoundException('document', f'{number}/{name}', logger)


def resolve_version(document: models.Document, version: int | None) -> models.DocumentVersion:
    """The version asked for, else the latest. 404 naming how many there are when it does not exist."""
    if version is None:
        return document.versions[-1]
    for each in document.versions:
        if each.version == version:
            return each
    raise HTTPException(
        status_code=status.HTTP_404_NOT_FOUND,
        detail=f'{document.address} has {len(document.versions)} versions, so there is no version {version}',
    )


def session_worklog(
    session: Session, request_session: str | None, host: str | None, now: dt.datetime
) -> tuple[str | None, models.Worklog | None]:
    """Record the agent session writing, and the live worklog it is in."""
    if request_session is None:
        return None, None
    record_session(session, request_session, host)
    return request_session, current_worklog(session, request_session, now)


def write_version(
    session: Session,
    number: int,
    name: str,
    request: schemas.DocumentWrite,
    written_by: str,
    now: dt.datetime,
    effects: WriteEffects,
) -> tuple[models.Document, bool]:
    """Write the document's next version, creating the document on its first, and say whether it was created.

    The record's row is locked while the version is counted, so two writes on one record never draw
    the same number or both create the document. A body matching the latest version writes nothing.
    """
    session.execute(select(models.Record.number).where(models.Record.number == number).with_for_update(key_share=True))
    document = find_document(session, number, name)
    created = document is None
    if document is None:
        if request.kind is None:
            raise HTTPException(
                status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
                detail=f'#{number} has no document {name!r} yet, so the write that creates it names its kind',
            )
        document = models.Document(record_number=number, name=name, kind=request.kind, created_ts=now)
        session.add(document)
        session.flush()
    elif request.kind is not None and request.kind != document.kind:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'{document.address} is a {document.kind} document, not a {request.kind}; name another to write a {request.kind}',
        )
    latest = document.versions[-1] if document.versions else None
    if latest is not None and latest.body == request.body:
        effects.warn(
            WriteEffectCode.DOCUMENT_UNCHANGED,
            f'Version {latest.version} of {document.address} already reads exactly this, so no version was written.',
            number,
        )
        return document, created
    agent_session, worklog = session_worklog(session, request.session, request.host, now)
    version = models.DocumentVersion(
        document=document,
        version=(latest.version if latest is not None else 0) + 1,
        body=request.body,
        written_by=written_by,
        session_id=agent_session,
        worklog_id=worklog.id if worklog is not None else None,
        written_ts=now,
    )
    session.add(version)
    if agent_session is not None and worklog is None:
        effects.warn(
            WriteEffectCode.NO_WORKLOG,
            f'Session {agent_session} is in no live worklog, so version {version.version} of {document.address} names none. '
            f'Hold #{number}, or start or join a worklog on it, so the next version names the go that wrote it.',
            number,
        )
    session.flush()
    logger.info('document_version_written', document=document.address, version=version.version, worklog=worklog.name if worklog else None)
    return document, created
