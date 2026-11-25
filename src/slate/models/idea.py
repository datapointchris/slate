from __future__ import annotations

import datetime as dt
from uuid import UUID
from uuid import uuid7

from sqlalchemy import CheckConstraint
from sqlalchemy import DateTime
from sqlalchemy import SmallInteger
from sqlalchemy import Text
from sqlalchemy import Uuid
from sqlalchemy import text
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import mapped_column

from slate.database.base import Base
from slate.models.record import Kind
from slate.models.record import NumberedRecord
from slate.models.record import record_link


class Idea(NumberedRecord, Base):
    """Work nobody has taken on yet: what it is, how urgent, and the refs it rests on.

    An idea is never work, so it has no status. Promoting it turns it into an initiative or an
    issue under its own number. Its priority is its own, set like any other record's.
    """

    RECORD_KIND = Kind.IDEA

    __tablename__ = 'ideas'
    id: Mapped[UUID] = mapped_column(Uuid, primary_key=True, default=uuid7)
    title: Mapped[str] = mapped_column(Text, nullable=False)
    description: Mapped[str | None] = mapped_column(Text, nullable=True)
    priority: Mapped[int] = mapped_column(SmallInteger, nullable=False, server_default='0')
    created_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))
    updated_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))

    __table_args__ = (
        *record_link(RECORD_KIND),
        CheckConstraint('priority BETWEEN 0 AND 4', name='idea_priority_range'),
    )

    def __repr__(self) -> str:
        return f'Idea(id={self.id!r}, number={self.number!r}, title={self.title!r})'
