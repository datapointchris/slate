"""The planning records and the one number every kind is addressed by.

Ideas, initiatives and issues are kinds of one record. `records` holds a row per number, saying
which kind holds it, and each kind's table references its row one to one. That is class table
inheritance. A claim, and anything else that attaches to "a record", holds one foreign key to
`records` whatever the kind.

Each kind's table carries the kind it is beside the number, and references `records` on both. So
the database refuses an issue whose number is recorded as an initiative's.

`rank` is every record's place in the one order `slate next` reads, so ideas, initiatives and
issues are placed against each other in one space. `services/fractional_rank.py` moves it.
"""

from __future__ import annotations

import datetime as dt
import enum
from typing import Any
from typing import ClassVar

from sqlalchemy import BigInteger
from sqlalchemy import CheckConstraint
from sqlalchemy import DateTime
from sqlalchemy import Double
from sqlalchemy import FetchedValue
from sqlalchemy import ForeignKey
from sqlalchemy import ForeignKeyConstraint
from sqlalchemy import Index
from sqlalchemy import Sequence
from sqlalchemy import Text
from sqlalchemy import UniqueConstraint
from sqlalchemy import select
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import column_property
from sqlalchemy.orm import declared_attr
from sqlalchemy.orm import mapped_column
from sqlalchemy.orm import relationship

from slate.database.base import Base

RECORD_NUMBER_SEQUENCE = Sequence('record_numbers', metadata=Base.metadata)


class Kind(enum.StrEnum):
    """Every kind of record, each one table. A dispatch on a kind names each member, so mypy finds the ones a new kind misses."""

    IDEA = 'idea'
    INITIATIVE = 'initiative'
    ISSUE = 'issue'


# Linear's scale, shared by every kind. 0 is the absence of a priority and sorts after every
# level, so the smallest non-zero value is the most urgent.
PRIORITIES = {0: 'none', 1: 'urgent', 2: 'high', 3: 'medium', 4: 'low'}


class RecordKind(Base):
    __tablename__ = 'record_kinds'
    name: Mapped[str] = mapped_column(Text, primary_key=True)


class Record(Base):
    """One number, and the kind of record that holds it."""

    __tablename__ = 'records'
    number: Mapped[int] = mapped_column(
        BigInteger, RECORD_NUMBER_SEQUENCE, primary_key=True, server_default=RECORD_NUMBER_SEQUENCE.next_value()
    )
    kind: Mapped[Kind] = mapped_column(Text, ForeignKey('record_kinds.name'), nullable=False)
    # Left unset, the `records_rank_at_end` trigger places the record one past the last, so every
    # way a row is inserted lands at the end.
    rank: Mapped[float] = mapped_column(Double, nullable=False, server_default=FetchedValue())
    # A person's pin, which sorts the record ahead of every priority until it is unpinned or closed.
    # `pinned_session` is the agent session that pinned it, where one did.
    pinned_ts: Mapped[dt.datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    pinned_by: Mapped[str | None] = mapped_column(Text, nullable=True)
    pinned_session: Mapped[str | None] = mapped_column(Text, ForeignKey('agent_sessions.id'), nullable=True)

    # The pair each kind's table references, so the kind it declares is the kind recorded here.
    __table_args__ = (
        UniqueConstraint('number', 'kind', name='uq_records_number_kind'),
        Index('idx_records_rank', 'rank'),
    )
    __mapper_args__ = {'eager_defaults': True}

    def __repr__(self) -> str:
        return f'Record(number={self.number!r}, kind={self.kind!r}, rank={self.rank!r})'


class NumberedRecord:
    """The columns that make a table one kind of planning record.

    A new row draws its number by creating its `records` row, so constructing one is enough. The
    unit of work inserts the `records` row first and copies its number across.
    """

    RECORD_KIND: ClassVar[Kind]

    number: Mapped[int] = mapped_column(BigInteger, nullable=False, unique=True)

    @declared_attr
    def record_kind(cls) -> Mapped[str]:
        return mapped_column(Text, nullable=False, server_default=cls.RECORD_KIND)

    @declared_attr
    def record(cls: type[NumberedRecord]) -> Mapped[Record]:
        return relationship(Record, foreign_keys=f'[{cls.__name__}.number, {cls.__name__}.record_kind]')

    @declared_attr
    def rank(cls: type[NumberedRecord]) -> Mapped[float]:
        """The record's rank, read with the row. A move writes `record.rank`."""
        return column_property(select(Record.rank).where(Record.number == cls.number).scalar_subquery())

    @declared_attr
    def pinned_ts(cls: type[NumberedRecord]) -> Mapped[dt.datetime | None]:
        """When a person pinned the record, read with the row. `services/record_pins.py` writes it on `record`."""
        return column_property(select(Record.pinned_ts).where(Record.number == cls.number).scalar_subquery())

    @declared_attr
    def pinned_by(cls: type[NumberedRecord]) -> Mapped[str | None]:
        return column_property(select(Record.pinned_by).where(Record.number == cls.number).scalar_subquery())

    @declared_attr
    def pinned_session(cls: type[NumberedRecord]) -> Mapped[str | None]:
        return column_property(select(Record.pinned_session).where(Record.number == cls.number).scalar_subquery())

    def __init__(self, **kwargs: Any) -> None:
        """`rank` places the new record; left out, it goes one past the last."""
        rank = kwargs.pop('rank', None)
        if 'record' not in kwargs and 'number' not in kwargs:
            placed = {} if rank is None else {'rank': rank}
            kwargs['record'] = Record(kind=self.RECORD_KIND, **placed)
        super().__init__(**kwargs)


def record_link(kind: Kind) -> tuple[CheckConstraint, ForeignKeyConstraint]:
    """The constraints a kind's `__table_args__` carries: its declared kind, and the link on both columns."""
    return (
        CheckConstraint(f"record_kind = '{kind}'", name='record_kind_is_declared'),
        ForeignKeyConstraint(['number', 'record_kind'], ['records.number', 'records.kind'], ondelete='CASCADE'),
    )
