"""Pursuits: the conditions the work should bring about and hold, and the ideas and initiatives serving each.

A pursuit is read from what is measured rather than from time spent, and `measure` names the
command that reads where it stands. It closes when it is met or dropped, which sets it apart from
a tag, a category with no end. It holds no work and lends no priority, so the queue never lists
one and a claim never holds one. Ideas and initiatives join pursuits many to many through
`record_pursuits`, which hangs off `records` by the member's number and off `pursuits` by its id.
An issue reads its initiative's pursuits, derived on every read.
"""

from __future__ import annotations

import datetime as dt
from uuid import UUID
from uuid import uuid7

from sqlalchemy import BigInteger
from sqlalchemy import CheckConstraint
from sqlalchemy import DateTime
from sqlalchemy import ForeignKey
from sqlalchemy import Index
from sqlalchemy import Text
from sqlalchemy import Uuid
from sqlalchemy import text
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import mapped_column

from slate.database.base import Base
from slate.models.record import Kind
from slate.models.record import NumberedRecord
from slate.models.record import record_link
from slate.models.record_change import KNOWN_BY

PURSUIT_STATUSES = ['active', 'someday', 'completed', 'dropped']
TERMINAL_PURSUIT_STATUSES = ['completed', 'dropped']


class PursuitStatus(Base):
    __tablename__ = 'pursuit_statuses'
    name: Mapped[str] = mapped_column(Text, primary_key=True)


class Pursuit(NumberedRecord, Base):
    """One condition the work should bring about and hold, and the command that reads where it stands."""

    RECORD_KIND = Kind.PURSUIT

    __tablename__ = 'pursuits'
    id: Mapped[UUID] = mapped_column(Uuid, primary_key=True, default=uuid7)
    # Unique among active pursuits only, so a finished pursuit's name can be reused.
    name: Mapped[str] = mapped_column(Text, nullable=False)
    # What the condition means, in one line.
    summary: Mapped[str] = mapped_column(Text, nullable=False)
    description: Mapped[str | None] = mapped_column(Text, nullable=True)
    # The command that reads where the pursuit stands, on one line. Null where nothing measures it yet.
    measure: Mapped[str | None] = mapped_column(Text, nullable=True)
    status: Mapped[str] = mapped_column(Text, ForeignKey('pursuit_statuses.name'), nullable=False, server_default='active')
    status_reason: Mapped[str | None] = mapped_column(Text, nullable=True)
    created_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))
    closed_ts: Mapped[dt.datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)

    __table_args__ = (
        *record_link(RECORD_KIND),
        CheckConstraint("status <> 'dropped' OR status_reason IS NOT NULL", name='pursuit_dropped_requires_reason'),
        Index('uq_pursuits_name_active', 'name', unique=True, postgresql_where=text("status = 'active'")),
        {'info': {KNOWN_BY: 'name'}},
    )

    def __repr__(self) -> str:
        return f'Pursuit(number={self.number!r}, name={self.name!r}, status={self.status!r})'


class RecordPursuit(Base):
    """An idea or an initiative in a pursuit. The API refuses any other kind of member."""

    __tablename__ = 'record_pursuits'
    record_number: Mapped[int] = mapped_column(BigInteger, ForeignKey('records.number', ondelete='CASCADE'), primary_key=True)
    pursuit_id: Mapped[UUID] = mapped_column(Uuid, ForeignKey('pursuits.id', ondelete='CASCADE'), primary_key=True)
    created_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))

    __table_args__ = (Index('idx_record_pursuits_pursuit', 'pursuit_id'),)

    def __repr__(self) -> str:
        return f'RecordPursuit(record_number={self.record_number!r}, pursuit_id={self.pursuit_id!r})'
