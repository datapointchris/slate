from __future__ import annotations

import datetime as dt
from typing import TYPE_CHECKING
from uuid import UUID
from uuid import uuid7

from sqlalchemy import CheckConstraint
from sqlalchemy import DateTime
from sqlalchemy import ForeignKey
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
    from slate.models.initiative import Initiative


class Idea(NumberedRecord, Base):
    """Work nobody has taken on yet: what it is, how urgent, and the evidence for it.

    An idea is never work, so it has no status. Promoting it creates the initiative that is the
    work, which names the idea back. Its priority is its own, set like any other record's.
    """

    RECORD_KIND = Kind.IDEA

    __tablename__ = 'ideas'
    id: Mapped[UUID] = mapped_column(Uuid, primary_key=True, default=uuid7)
    title: Mapped[str] = mapped_column(Text, nullable=False)
    description: Mapped[str | None] = mapped_column(Text, nullable=True)
    priority: Mapped[int] = mapped_column(SmallInteger, nullable=False, server_default='0')
    created_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))
    updated_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))

    evidence: Mapped[list[IdeaEvidence]] = relationship(
        back_populates='idea', cascade='all, delete-orphan', order_by='IdeaEvidence.linked_ts, IdeaEvidence.ref'
    )
    initiative: Mapped[Initiative | None] = relationship('Initiative', back_populates='idea')

    __table_args__ = (
        *record_link(RECORD_KIND),
        CheckConstraint('priority BETWEEN 0 AND 4', name='idea_priority_range'),
    )

    def __repr__(self) -> str:
        return f'Idea(id={self.id!r}, number={self.number!r}, title={self.title!r})'


class IdeaEvidence(Base):
    """One thing an idea rests on, named by a key or a URL that slate never follows.

    A key is whatever the system holding the evidence calls it, such as an inbox row's. So slate
    knows nothing of where the evidence lives, and a link outlives any one source.
    """

    __tablename__ = 'idea_evidence'
    idea_id: Mapped[UUID] = mapped_column(Uuid, ForeignKey('ideas.id', ondelete='CASCADE'), primary_key=True)
    ref: Mapped[str] = mapped_column(Text, primary_key=True)
    note: Mapped[str | None] = mapped_column(Text, nullable=True)
    linked_by: Mapped[str | None] = mapped_column(Text, nullable=True)
    linked_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))

    idea: Mapped[Idea] = relationship(back_populates='evidence')

    __table_args__ = (CheckConstraint("btrim(ref) <> ''", name='ref_not_blank'),)
