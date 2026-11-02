from __future__ import annotations

import datetime as dt
from typing import TYPE_CHECKING
from uuid import UUID
from uuid import uuid7

from sqlalchemy import Boolean
from sqlalchemy import CheckConstraint
from sqlalchemy import DateTime
from sqlalchemy import ForeignKey
from sqlalchemy import Index
from sqlalchemy import Integer
from sqlalchemy import SmallInteger
from sqlalchemy import Text
from sqlalchemy import Uuid
from sqlalchemy import text
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import mapped_column
from sqlalchemy.orm import relationship

from slate.database.base import Base
from slate.models.record import Kind
from slate.models.record import NumberedRecord
from slate.models.record import record_link

if TYPE_CHECKING:
    from slate.models.idea import Idea
    from slate.models.issue import Issue

# `someday` holds ideas not yet taken on: its issues wait until the initiative is
# active, except one that work outside someday depends on, and it lends no priority.
INITIATIVE_STATUSES = ['active', 'someday', 'completed', 'dropped']
TERMINAL_INITIATIVE_STATUSES = ['completed', 'dropped']


class InitiativeStatus(Base):
    __tablename__ = 'initiative_statuses'
    name: Mapped[str] = mapped_column(Text, primary_key=True)


class Initiative(NumberedRecord, Base):
    """A bounded outcome that groups issues, and the priority they inherit.

    An initiative has a definition of done. Work with no end is a label instead,
    because an initiative that can never finish holds its issues' place in the
    queue forever. Only an active initiative lends its priority and keeps its
    issues together in the queue, so closing one releases the issues still open
    inside it.
    """

    RECORD_KIND = Kind.INITIATIVE

    __tablename__ = 'initiatives'
    id: Mapped[UUID] = mapped_column(Uuid, primary_key=True, default=uuid7)
    # Unique among ACTIVE initiatives only, so a finished initiative's name can be reused.
    name: Mapped[str] = mapped_column(Text, nullable=False)
    description: Mapped[str | None] = mapped_column(Text, nullable=True)
    status: Mapped[str] = mapped_column(Text, ForeignKey('initiative_statuses.name'), nullable=False, server_default='active')
    status_reason: Mapped[str | None] = mapped_column(Text, nullable=True)
    priority: Mapped[int] = mapped_column(SmallInteger, nullable=False, server_default='0')
    # False keeps every issue in it from an unattended fix, so a step of one
    # design is never landed alone.
    autofix: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True, server_default=text('true'))
    # The order the API release before `records.rank` reads, kept while the two can overlap in a
    # rollout. Nothing here reads or writes it.
    previous_release_position: Mapped[int] = mapped_column('position', Integer, nullable=False, server_default='0')
    created_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))
    closed_ts: Mapped[dt.datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    # The idea promoted into this initiative. One idea becomes at most one initiative.
    idea_id: Mapped[UUID | None] = mapped_column(Uuid, ForeignKey('ideas.id', ondelete='SET NULL'), nullable=True, unique=True)

    issues: Mapped[list[Issue]] = relationship('Issue', back_populates='initiative')
    idea: Mapped[Idea | None] = relationship('Idea', back_populates='initiative')

    __table_args__ = (
        *record_link(RECORD_KIND),
        CheckConstraint("status <> 'dropped' OR status_reason IS NOT NULL", name='initiative_dropped_requires_reason'),
        CheckConstraint('priority BETWEEN 0 AND 4', name='initiative_priority_range'),
        Index('uq_initiatives_name_active', 'name', unique=True, postgresql_where=text("status = 'active'")),
    )

    def __repr__(self):
        return f'Initiative(id={self.id!r}, name={self.name!r}, status={self.status!r})'
