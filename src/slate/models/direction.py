"""Directions: the ways the work moves toward one vision, and the ideas and initiatives in each.

A direction closes when the work gets where it says, which sets it apart from a tag, a category
with no end. It holds no work and lends no priority, so the queue never lists one and a claim never
holds one. Ideas and initiatives join directions many to many through `record_directions`, which
hangs off `records` by the member's number and off `directions` by its id. An issue reads its
initiative's directions, derived on every read.
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

DIRECTION_STATUSES = ['active', 'someday', 'completed', 'dropped']
TERMINAL_DIRECTION_STATUSES = ['completed', 'dropped']


class DirectionStatus(Base):
    __tablename__ = 'direction_statuses'
    name: Mapped[str] = mapped_column(Text, primary_key=True)


class Direction(NumberedRecord, Base):
    """One way the work moves toward the vision, and where it ends."""

    RECORD_KIND = Kind.DIRECTION

    __tablename__ = 'directions'
    id: Mapped[UUID] = mapped_column(Uuid, primary_key=True, default=uuid7)
    # Unique among active directions only, so a finished direction's name can be reused.
    name: Mapped[str] = mapped_column(Text, nullable=False)
    # Where the direction ends, in one line.
    summary: Mapped[str] = mapped_column(Text, nullable=False)
    description: Mapped[str | None] = mapped_column(Text, nullable=True)
    status: Mapped[str] = mapped_column(Text, ForeignKey('direction_statuses.name'), nullable=False, server_default='active')
    status_reason: Mapped[str | None] = mapped_column(Text, nullable=True)
    created_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))
    closed_ts: Mapped[dt.datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)

    __table_args__ = (
        *record_link(RECORD_KIND),
        CheckConstraint("status <> 'dropped' OR status_reason IS NOT NULL", name='direction_dropped_requires_reason'),
        Index('uq_directions_name_active', 'name', unique=True, postgresql_where=text("status = 'active'")),
        {'info': {KNOWN_BY: 'name'}},
    )

    def __repr__(self) -> str:
        return f'Direction(number={self.number!r}, name={self.name!r}, status={self.status!r})'


class RecordDirection(Base):
    """An idea or an initiative in a direction. The API refuses any other kind of member."""

    __tablename__ = 'record_directions'
    record_number: Mapped[int] = mapped_column(BigInteger, ForeignKey('records.number', ondelete='CASCADE'), primary_key=True)
    direction_id: Mapped[UUID] = mapped_column(Uuid, ForeignKey('directions.id', ondelete='CASCADE'), primary_key=True)
    created_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))

    __table_args__ = (Index('idx_record_directions_direction', 'direction_id'),)

    def __repr__(self) -> str:
        return f'RecordDirection(record_number={self.record_number!r}, direction_id={self.direction_id!r})'
