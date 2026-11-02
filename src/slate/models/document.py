"""A record's research, design and plan documents, each a series of versions never edited once written.

A document is named on its record, so `1811/plan` is record 1811's plan. Writing it adds the next
version. Each version names who wrote it, the agent session that did, and the worklog that session
was in, so a later go at the record reads which go produced which version.
"""

from __future__ import annotations

import datetime as dt
from typing import Literal
from typing import get_args

from sqlalchemy import BigInteger
from sqlalchemy import CheckConstraint
from sqlalchemy import DateTime
from sqlalchemy import ForeignKey
from sqlalchemy import Identity
from sqlalchemy import Index
from sqlalchemy import Integer
from sqlalchemy import Text
from sqlalchemy import UniqueConstraint
from sqlalchemy import text
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import mapped_column
from sqlalchemy.orm import relationship

from slate.database.base import Base
from slate.models.record import Record

# What a document is for. A worklog that wrote a plan has reached its `planned` stage.
DocumentKindName = Literal['research', 'design', 'plan']
DOCUMENT_KINDS: list[str] = list(get_args(DocumentKindName))
PLAN: DocumentKindName = 'plan'

# A document's name, unique on its record. The CLI names a document by its kind until a record
# holds two of one kind.
DOCUMENT_NAME_PATTERN = '^[a-z0-9]+(-[a-z0-9]+)*$'


class DocumentKind(Base):
    __tablename__ = 'document_kinds'
    name: Mapped[str] = mapped_column(Text, primary_key=True)


class Document(Base):
    __tablename__ = 'documents'
    id: Mapped[int] = mapped_column(BigInteger, Identity(), primary_key=True)
    record_number: Mapped[int] = mapped_column(BigInteger, ForeignKey('records.number', ondelete='CASCADE'), nullable=False)
    name: Mapped[str] = mapped_column(Text, nullable=False)
    kind: Mapped[str] = mapped_column(Text, ForeignKey('document_kinds.name'), nullable=False)
    created_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))

    record: Mapped[Record] = relationship(Record)
    versions: Mapped[list[DocumentVersion]] = relationship(back_populates='document', order_by='DocumentVersion.version')

    __table_args__ = (
        UniqueConstraint('record_number', 'name', name='uq_documents_one_name_on_a_record'),
        # The pair a line comment references, so the database refuses one naming another record's document.
        UniqueConstraint('record_number', 'id', name='uq_documents_record_number_id'),
        CheckConstraint(f"name ~ '{DOCUMENT_NAME_PATTERN}'", name='document_name_shape'),
    )

    @property
    def address(self) -> str:
        return f'{self.record_number}/{self.name}'

    def __repr__(self) -> str:
        return f'Document(address={self.address!r}, kind={self.kind!r})'


class DocumentVersion(Base):
    """One version of a document's body, numbered from one, and who wrote it from where."""

    __tablename__ = 'document_versions'
    document_id: Mapped[int] = mapped_column(BigInteger, ForeignKey('documents.id', ondelete='CASCADE'), primary_key=True)
    version: Mapped[int] = mapped_column(Integer, primary_key=True)
    body: Mapped[str] = mapped_column(Text, nullable=False)
    # The caller's subject or client id, as `bind_actor` records it.
    written_by: Mapped[str] = mapped_column(Text, nullable=False)
    session_id: Mapped[str | None] = mapped_column(Text, ForeignKey('agent_sessions.id'), nullable=True)
    # The worklog the session was in when it wrote the version. Null when it was in none, or no session wrote it.
    worklog_id: Mapped[int | None] = mapped_column(BigInteger, ForeignKey('worklogs.id', ondelete='SET NULL'), nullable=True)
    written_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))

    document: Mapped[Document] = relationship(back_populates='versions')

    __table_args__ = (
        CheckConstraint('version >= 1', name='version_counts_from_one'),
        CheckConstraint("btrim(body) <> ''", name='document_body_not_blank'),
        Index('idx_document_versions_worklog_id', 'worklog_id'),
    )

    @property
    def lines(self) -> list[str]:
        """The body split at each newline alone, numbered from one by a line comment. A final newline ends the last line."""
        lines = self.body.split('\n')
        return lines[:-1] if lines[-1] == '' else lines

    def __repr__(self) -> str:
        return f'DocumentVersion(document_id={self.document_id!r}, version={self.version!r})'
