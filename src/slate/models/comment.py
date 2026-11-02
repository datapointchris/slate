"""A comment on a record of any kind, or on one line of one version of a record's document.

A comment names who wrote it, the agent session that did, and the worklog that session was in. A
line comment stays on the version and line it was written on. `services/document_views.py` carries
it onto any other version read.
"""

from __future__ import annotations

import datetime as dt

from sqlalchemy import BigInteger
from sqlalchemy import CheckConstraint
from sqlalchemy import DateTime
from sqlalchemy import ForeignKey
from sqlalchemy import ForeignKeyConstraint
from sqlalchemy import Identity
from sqlalchemy import Index
from sqlalchemy import Integer
from sqlalchemy import Text
from sqlalchemy import text
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import mapped_column

from slate.database.base import Base


class Comment(Base):
    __tablename__ = 'comments'
    id: Mapped[int] = mapped_column(BigInteger, Identity(), primary_key=True)
    record_number: Mapped[int] = mapped_column(BigInteger, ForeignKey('records.number', ondelete='CASCADE'), nullable=False)
    # The document version and line a line comment sits on. All three are null on a comment on the record.
    document_id: Mapped[int | None] = mapped_column(BigInteger, nullable=True)
    version: Mapped[int | None] = mapped_column(Integer, nullable=True)
    line: Mapped[int | None] = mapped_column(Integer, nullable=True)
    body: Mapped[str] = mapped_column(Text, nullable=False)
    # The caller's subject or client id. Null when nobody recorded who wrote it.
    written_by: Mapped[str | None] = mapped_column(Text, nullable=True)
    session_id: Mapped[str | None] = mapped_column(Text, ForeignKey('agent_sessions.id'), nullable=True)
    # The worklog the session was in when it wrote the comment.
    worklog_id: Mapped[int | None] = mapped_column(BigInteger, ForeignKey('worklogs.id', ondelete='SET NULL'), nullable=True)
    written_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))

    __table_args__ = (
        ForeignKeyConstraint(['record_number', 'document_id'], ['documents.record_number', 'documents.id'], ondelete='CASCADE'),
        ForeignKeyConstraint(
            ['document_id', 'version'], ['document_versions.document_id', 'document_versions.version'], ondelete='CASCADE'
        ),
        CheckConstraint('(document_id IS NULL) = (version IS NULL) AND (version IS NULL) = (line IS NULL)', name='comment_line_is_whole'),
        CheckConstraint('line IS NULL OR line >= 1', name='comment_line_counts_from_one'),
        CheckConstraint("btrim(body) <> ''", name='comment_body_not_blank'),
        Index('idx_comments_record_number', 'record_number'),
        Index('idx_comments_document_id_version', 'document_id', 'version'),
    )

    def __repr__(self) -> str:
        return f'Comment(id={self.id!r}, record_number={self.record_number!r}, line={self.line!r})'
