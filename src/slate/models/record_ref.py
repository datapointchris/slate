"""Refs: what a record came from or rests on, named by a key or a URL that slate never follows.

A key is whatever the system holding the thing calls it, such as an inbox row's `capture/<id>`.
So slate knows nothing of where it lives, and a link outlives any one source. `record_refs` hangs
off `records` by number, so an idea, an initiative and an issue each carry refs through one
table, and deleting the record deletes its rows.

The link is written once, on the record that makes the reference, and a reader finds it from the
other side by prefix: every record whose refs start with `capture/`. `idx_record_refs_ref_prefix`
serves that read under any collation.
"""

from __future__ import annotations

import datetime as dt

from sqlalchemy import BigInteger
from sqlalchemy import CheckConstraint
from sqlalchemy import DateTime
from sqlalchemy import ForeignKey
from sqlalchemy import Index
from sqlalchemy import Text
from sqlalchemy import text
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import mapped_column

from slate.database.base import Base


class RecordRef(Base):
    """One ref that a record carries, with what it shows and who linked it."""

    __tablename__ = 'record_refs'
    record_number: Mapped[int] = mapped_column(BigInteger, ForeignKey('records.number', ondelete='CASCADE'), primary_key=True)
    ref: Mapped[str] = mapped_column(Text, primary_key=True)
    note: Mapped[str | None] = mapped_column(Text, nullable=True)
    linked_by: Mapped[str | None] = mapped_column(Text, nullable=True)
    linked_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))

    __table_args__ = (
        CheckConstraint("btrim(ref) <> ''", name='ref_not_blank'),
        Index('idx_record_refs_ref_prefix', 'ref', postgresql_ops={'ref': 'text_pattern_ops'}),
    )

    def __repr__(self) -> str:
        return f'RecordRef(record_number={self.record_number!r}, ref={self.ref!r})'
