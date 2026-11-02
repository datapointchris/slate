"""Comments on any record, or on one line of a version of its document: added, read oldest first, and deleted.

A comment names who wrote it from the caller's token, and the agent session and worklog it came
from by reference. A scoped client comments as its own client id.
"""

import datetime as dt
from typing import Annotated

import structlog
from fastapi import APIRouter
from fastapi import HTTPException
from fastapi import Query
from fastapi import status
from sqlalchemy import select

from slate import models
from slate import schemas
from slate.api.callers import CurrentCaller
from slate.api.callers import DbSession
from slate.api.callers import caller_actor
from slate.api.exceptions import NotFoundException
from slate.services.comment_views import COMMENT_ORDER
from slate.services.comment_views import comment_views
from slate.services.documents import resolve_document
from slate.services.documents import resolve_version
from slate.services.documents import session_worklog
from slate.services.record_refs import resolve_record
from slate.services.row_limit import RowLimit
from slate.services.row_limit import apply_row_limit
from slate.services.write_effects import Effects
from slate.tracing.work_on_span import name_agent_session
from slate.tracing.work_on_span import name_record
from slate.tracing.work_on_span import name_worklog

logger = structlog.get_logger()
router = APIRouter()


@router.get('', response_model=list[schemas.Comment], status_code=status.HTTP_200_OK)
def read_many(
    session: DbSession,
    record: Annotated[str, Query(description='The record whose comments to read, by number')],
    document: Annotated[str | None, Query(description="Only the line comments on this one of the record's documents")] = None,
    limit: RowLimit = None,
):
    """A record's comments oldest first, so a thread reads in the order it was written. Line comments on its documents are among them."""
    number = resolve_record(session, record).number
    name_record(number)
    query = select(models.Comment).where(models.Comment.record_number == number).order_by(*COMMENT_ORDER)
    if document is not None:
        query = query.where(models.Comment.document_id == resolve_document(session, number, document).id)
    return comment_views(session, session.scalars(apply_row_limit(query, limit)).all())


@router.post('', response_model=schemas.CommentWritten, status_code=status.HTTP_201_CREATED)
def add(request: schemas.CommentAdd, session: DbSession, caller: CurrentCaller, effects: Effects):
    """Comment on a record, or on one line of its document's latest version or the version named.

    The comment names the caller, the agent session writing it, and the worklog that session is in.
    A line past the version's last is a 422.
    """
    number = resolve_record(session, request.record).number
    name_record(number)
    name_agent_session(request.session)
    now = dt.datetime.now(dt.UTC)
    comment = models.Comment(record_number=number, body=request.body, written_by=caller_actor(caller), written_ts=now)
    if request.document is not None and request.line is not None:
        document = resolve_document(session, number, request.document)
        version = resolve_version(document, request.version)
        if request.line > len(version.lines):
            raise HTTPException(
                status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
                detail=f'Version {version.version} of {document.address} has {len(version.lines)} lines, so no line {request.line}',
            )
        comment.document_id, comment.version, comment.line = document.id, version.version, request.line
    agent_session, worklog = session_worklog(session, request.session, request.host, now)
    comment.session_id = agent_session
    comment.worklog_id = worklog.id if worklog is not None else None
    if worklog is not None:
        name_worklog(worklog)
    session.add(comment)
    session.commit()
    logger.info('comment_added', record=number, comment=comment.id, document=request.document, line=request.line)
    return effects.answer(schemas.CommentWritten, comment=comment_views(session, [comment])[0])


@router.delete('/{id}', response_model=schemas.CommentDeleted, status_code=status.HTTP_200_OK)
def delete(id: int, session: DbSession, effects: Effects):
    """Delete one comment, answering it as it stood."""
    comment = session.get(models.Comment, id)
    if comment is None:
        raise NotFoundException('comment', str(id), logger)
    name_record(comment.record_number)
    deleted = comment_views(session, [comment])[0]
    session.delete(comment)
    session.commit()
    logger.info('comment_deleted', record=deleted.record_number, comment=id)
    return effects.answer(schemas.CommentDeleted, deleted=deleted)
