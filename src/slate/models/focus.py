"""A focus: the direction the work is stated to take for a while, holding the share of it each pursuit should get.

One focus is in force at a time. Setting another ends it as `replaced`, and ending one stamps when
and why. A focus whose `until_date` has passed reads as ended though nothing wrote its end, so no
job sweeps it. Every focus stays, so the history of the direction is a read.

A share is a fraction of the work from 0 to 1, named for one pursuit. The pursuits a focus leaves
unnamed split the rest of 1 between them on read, which `services/focus_standing.py` derives.
"""

from __future__ import annotations

import datetime as dt
from decimal import Decimal
from uuid import UUID

from sqlalchemy import BigInteger
from sqlalchemy import CheckConstraint
from sqlalchemy import Date
from sqlalchemy import DateTime
from sqlalchemy import ForeignKey
from sqlalchemy import Identity
from sqlalchemy import Index
from sqlalchemy import Numeric
from sqlalchemy import Text
from sqlalchemy import Uuid
from sqlalchemy import text
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import mapped_column
from sqlalchemy.orm import relationship

from slate.database.base import Base
from slate.models.pursuit import Pursuit

# The partial unique index that holds one unended focus, so two sets racing cannot both insert.
ONE_UNENDED_FOCUS = 'uq_focuses_one_unended'

# The reason a set writes on the focus it replaces, and on one whose `until_date` had already passed.
REPLACED = 'replaced'
LAPSED = 'lapsed'


class Focus(Base):
    __tablename__ = 'focuses'
    id: Mapped[int] = mapped_column(BigInteger, Identity(), primary_key=True)
    # The direction in the words of whoever set it.
    statement: Mapped[str] = mapped_column(Text, nullable=False)
    started_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))
    # The last day it holds. A focus reads as ended from the day after.
    until_date: Mapped[dt.date] = mapped_column(Date, nullable=False)
    ended_ts: Mapped[dt.datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    ended_reason: Mapped[str | None] = mapped_column(Text, nullable=True)
    # The caller that set it: a person's username, else their `sub`, or a service's client id.
    set_by: Mapped[str] = mapped_column(Text, nullable=False)

    shares: Mapped[list[FocusShare]] = relationship(back_populates='focus', lazy='selectin')

    __table_args__ = (
        CheckConstraint('(ended_ts IS NULL) = (ended_reason IS NULL)', name='focus_ending_is_whole'),
        Index(ONE_UNENDED_FOCUS, text('(true)'), unique=True, postgresql_where=text('ended_ts IS NULL')),
    )

    def __repr__(self) -> str:
        return f'Focus(id={self.id!r}, until_date={self.until_date!r}, ended_ts={self.ended_ts!r})'


class FocusShare(Base):
    """The share of the work a focus names for one pursuit, as a fraction from 0 to 1."""

    __tablename__ = 'focus_shares'
    focus_id: Mapped[int] = mapped_column(BigInteger, ForeignKey('focuses.id', ondelete='CASCADE'), primary_key=True)
    pursuit_id: Mapped[UUID] = mapped_column(Uuid, ForeignKey('pursuits.id', ondelete='CASCADE'), primary_key=True)
    share: Mapped[Decimal] = mapped_column(Numeric, nullable=False)

    focus: Mapped[Focus] = relationship(back_populates='shares')
    # Every read of a share names its pursuit, so it loads with the share.
    pursuit: Mapped[Pursuit] = relationship(Pursuit, lazy='selectin')

    __table_args__ = (
        CheckConstraint('share >= 0 AND share <= 1', name='focus_share_is_a_fraction'),
        Index('idx_focus_shares_pursuit', 'pursuit_id'),
    )

    def __repr__(self) -> str:
        return f'FocusShare(focus_id={self.focus_id!r}, pursuit_id={self.pursuit_id!r}, share={self.share!r})'
