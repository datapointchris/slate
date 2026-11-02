"""Read comments back, each with who wrote it, from which session and worklog, and the line it sits on."""

from __future__ import annotations

from collections.abc import Sequence

from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.services.worklog_names import worklog_names

COMMENT_ORDER = (models.Comment.written_ts, models.Comment.id)


def comment_lines(session: Session, comments: Sequence[models.Comment]) -> dict[tuple[int, int], tuple[models.Document, list[str]]]:
    """The document and lines of each version a line comment is on, keyed by document and version."""
    keys = {(comment.document_id, comment.version) for comment in comments if comment.document_id is not None}
    if not keys:
        return {}
    rows = session.execute(
        select(models.Document, models.DocumentVersion)
        .join(models.DocumentVersion, models.DocumentVersion.document_id == models.Document.id)
        .where(models.Document.id.in_({document for document, _ in keys}))
    )
    return {
        (version.document_id, version.version): (document, version.lines)
        for document, version in rows
        if (version.document_id, version.version) in keys
    }


def comment_view(
    comment: models.Comment, worklogs: dict[int, str], lines: dict[tuple[int, int], tuple[models.Document, list[str]]]
) -> schemas.Comment:
    on = None
    if comment.document_id is not None and comment.version is not None and comment.line is not None:
        document, text = lines[(comment.document_id, comment.version)]
        on = schemas.CommentLine(
            document=document.name, kind=document.kind, version=comment.version, line=comment.line, text=text[comment.line - 1]
        )
    return schemas.Comment(
        id=comment.id,
        record_number=comment.record_number,
        body=comment.body,
        written_by=comment.written_by,
        session=comment.session_id,
        worklog=worklogs.get(comment.worklog_id) if comment.worklog_id is not None else None,
        written_ts=comment.written_ts,
        on=on,
    )


def comment_views(session: Session, comments: Sequence[models.Comment]) -> list[schemas.Comment]:
    worklogs = worklog_names(session, (comment.worklog_id for comment in comments))
    lines = comment_lines(session, comments)
    return [comment_view(comment, worklogs, lines) for comment in comments]


def record_comments(session: Session, number: int) -> list[schemas.Comment]:
    """The comments on the record itself, oldest first. A line comment is read with its document."""
    query = (
        select(models.Comment).where(models.Comment.record_number == number, models.Comment.document_id.is_(None)).order_by(*COMMENT_ORDER)
    )
    return comment_views(session, session.scalars(query).all())
