"""Tags: a short closed list of categories with no end, and the records carrying each.

A tag exists before a record carries it, so a misspelling is refused rather than becoming a second
tag. `record_tags` hangs off `records` by number, so an idea, an initiative and an issue each carry
tags through one table, and deleting the record deletes its rows. A row names its tag by id, so a
rename writes the tag's row alone.
"""

from __future__ import annotations

import datetime as dt

from sqlalchemy import BigInteger
from sqlalchemy import CheckConstraint
from sqlalchemy import DateTime
from sqlalchemy import ForeignKey
from sqlalchemy import Identity
from sqlalchemy import Index
from sqlalchemy import Integer
from sqlalchemy import Text
from sqlalchemy import text
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import mapped_column

from slate.database.base import Base
from slate.models.record_change import KNOWN_BY

TAG_NAME_PATTERN = '^[a-z0-9]+(-[a-z0-9]+)*$'


class Tag(Base):
    __tablename__ = 'tags'
    id: Mapped[int] = mapped_column(Integer, Identity(always=True), primary_key=True)
    name: Mapped[str] = mapped_column(Text, nullable=False, unique=True)
    # When the tag applies, on one line `slate tags list` prints beside it.
    description: Mapped[str] = mapped_column(Text, nullable=False)
    created_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))

    __table_args__ = (CheckConstraint(f"name ~ '{TAG_NAME_PATTERN}'", name='tag_name_shape'), {'info': {KNOWN_BY: 'name'}})

    def __repr__(self) -> str:
        return f'Tag(name={self.name!r})'


class RecordTag(Base):
    """A record carrying a tag. An issue also reads as carrying its initiative's, which `services/record_tags.py` derives."""

    __tablename__ = 'record_tags'
    record_number: Mapped[int] = mapped_column(BigInteger, ForeignKey('records.number', ondelete='CASCADE'), primary_key=True)
    tag_id: Mapped[int] = mapped_column(Integer, ForeignKey('tags.id', ondelete='CASCADE'), primary_key=True)
    created_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))

    __table_args__ = (Index('idx_record_tags_tag', 'tag_id'),)

    def __repr__(self) -> str:
        return f'RecordTag(record_number={self.record_number!r}, tag_id={self.tag_id!r})'
