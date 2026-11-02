"""Read documents back: each version with who wrote it, and every line comment placed on the version read.

A line comment stays on the version and line it was written on. Read with another version, it sits
on the line its own became, found by diffing the two versions' lines, and on no line when that
version changed or removed it. A code review carries a comment onto a later revision the same way:
GitHub marks one whose line changed as outdated, and Gerrit ports it to the next patch set.
"""

from __future__ import annotations

import difflib
from collections import defaultdict
from collections.abc import Iterable
from collections.abc import Sequence
from typing import cast

from sqlalchemy import func
from sqlalchemy import select
from sqlalchemy.orm import Session
from sqlalchemy.orm import selectinload

from slate import models
from slate import schemas
from slate.services.comment_views import COMMENT_ORDER
from slate.services.comment_views import comment_view
from slate.services.record_summaries import record_summaries
from slate.services.worklog_names import worklog_names

DOCUMENT_LOAD_OPTIONS = (selectinload(models.Document.versions),)


def placed_line(written: Sequence[str], shown: Sequence[str], line: int) -> int | None:
    """The line of `shown` that line `line` of `written` is, or None when `shown` changed or removed it.

    Junk detection is off, so a blank line or a repeated one matches like any other.
    """
    index = line - 1
    for tag, start, end, shown_start, _ in difflib.SequenceMatcher(None, written, shown, autojunk=False).get_opcodes():
        if tag == 'equal' and start <= index < end:
            return shown_start + index - start + 1
    return None


def shown_line(
    comment: models.Comment, lines: dict[tuple[int, int], tuple[models.Document, list[str]]], shown: models.DocumentVersion
) -> int | None:
    """The line of `shown` a line comment sits on: its own on the version it was written on, else the one it became."""
    if comment.version is None or comment.line is None:
        return None
    if comment.version == shown.version:
        return comment.line
    return placed_line(lines[(shown.document_id, comment.version)][1], shown.lines, comment.line)


def version_summary(version: models.DocumentVersion, worklogs: dict[int, str]) -> schemas.DocumentVersionSummary:
    return schemas.DocumentVersionSummary(
        version=version.version,
        written_by=version.written_by,
        session=version.session_id,
        worklog=worklogs.get(version.worklog_id) if version.worklog_id is not None else None,
        written_ts=version.written_ts,
        line_count=len(version.lines),
    )


def document_view(session: Session, document: models.Document, shown: models.DocumentVersion) -> schemas.Document:
    comments = session.scalars(select(models.Comment).where(models.Comment.document_id == document.id).order_by(*COMMENT_ORDER)).all()
    worklogs = worklog_names(
        session, [*(version.worklog_id for version in document.versions), *(comment.worklog_id for comment in comments)]
    )
    lines = {(document.id, version.version): (document, version.lines) for version in document.versions}
    placed = [
        schemas.PlacedComment(**comment_view(comment, worklogs, lines).model_dump(), shown_line=shown_line(comment, lines, shown))
        for comment in comments
    ]
    return schemas.Document(
        record=record_summaries(session, [document.record_number])[document.record_number],
        name=document.name,
        address=document.address,
        kind=document.kind,
        created_ts=document.created_ts,
        version=schemas.DocumentVersion(**version_summary(shown, worklogs).model_dump(), body=shown.body),
        versions=[version_summary(version, worklogs) for version in document.versions],
        comments=placed,
    )


def document_summaries(session: Session, documents: Sequence[models.Document]) -> list[schemas.DocumentSummary]:
    """Each document with its latest version and its count of line comments. Each needs its versions loaded."""
    ids = [document.id for document in documents]
    counts = dict(
        session.execute(
            select(models.Comment.document_id, func.count()).where(models.Comment.document_id.in_(ids)).group_by(models.Comment.document_id)
        ).all()
    )
    worklogs = worklog_names(session, (document.versions[-1].worklog_id for document in documents))
    return [
        schemas.DocumentSummary(
            record_number=document.record_number,
            name=document.name,
            address=document.address,
            kind=document.kind,
            created_ts=document.created_ts,
            latest=version_summary(document.versions[-1], worklogs),
            comment_count=counts.get(document.id, 0),
        )
        for document in documents
    ]


def record_documents(session: Session, number: int) -> list[schemas.DocumentSummary]:
    """The record's documents in the order they were started."""
    query = (
        select(models.Document)
        .options(*DOCUMENT_LOAD_OPTIONS)
        .where(models.Document.record_number == number)
        .order_by(models.Document.created_ts, models.Document.id)
    )
    return document_summaries(session, session.scalars(query).all())


def worklog_document_versions(session: Session, worklog_ids: Iterable[int]) -> dict[int, list[schemas.DocumentVersionRef]]:
    """The document versions written in each worklog, oldest first."""
    rows = session.execute(
        select(models.DocumentVersion.worklog_id, models.DocumentVersion.version, models.DocumentVersion.written_ts, models.Document)
        .join(models.Document, models.Document.id == models.DocumentVersion.document_id)
        .where(models.DocumentVersion.worklog_id.in_(list(worklog_ids)))
        .order_by(models.DocumentVersion.written_ts, models.Document.id, models.DocumentVersion.version)
    )
    written: dict[int, list[schemas.DocumentVersionRef]] = defaultdict(list)
    for worklog_id, version, written_ts, document in rows:
        # The filter on worklog_id leaves no null one.
        written[cast(int, worklog_id)].append(
            schemas.DocumentVersionRef(address=document.address, kind=document.kind, version=version, written_ts=written_ts)
        )
    return written
