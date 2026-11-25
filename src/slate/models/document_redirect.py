"""An address a document held before a fold moved it to another record, so a read through that address still finds it.

A fold moves an idea's documents to the target, and renames one whose name the target already
holds. Each row maps the retired number and the name the document held there to the document
itself, so a later fold that moves it again leaves the row true and no chain forms.
"""

from __future__ import annotations

import datetime as dt

from sqlalchemy import BigInteger
from sqlalchemy import DateTime
from sqlalchemy import ForeignKey
from sqlalchemy import Index
from sqlalchemy import Text
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import mapped_column

from slate.database.base import Base


class DocumentRedirect(Base):
    __tablename__ = 'document_redirects'
    # The document's former record, by a number that a fold retired, so no `records` row holds it.
    record_number: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=False)
    name: Mapped[str] = mapped_column(Text, primary_key=True)
    document_id: Mapped[int] = mapped_column(BigInteger, ForeignKey('documents.id', ondelete='CASCADE'), nullable=False)
    redirected_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False)

    __table_args__ = (Index('idx_document_redirects_document_id', 'document_id'),)

    def __repr__(self) -> str:
        return f'DocumentRedirect(record_number={self.record_number!r}, name={self.name!r}, document_id={self.document_id!r})'
