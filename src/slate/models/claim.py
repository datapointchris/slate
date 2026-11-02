"""A claim is a lease on one record of any kind, kept after it ends.

A claim holds no work and writes nothing on the record it names. It says who is
on the record, where they work, and until when. Every claim stays: ending one
stamps how and when, so a record's claims read as its history of who held it.

A claim runs until it ends or its `expires_ts` passes. One that passed its
expiry and was never ended is expired, which frees the record for another
holder. Taking it over, or closing the record, ends it as expired at the moment
it expired. `services/claims.py` holds every write.

Holding a claim opens a worklog, and ending the claim ends it. A holder that
stops to wait on something says so with a wait, kept after it ends like the
claim itself.
"""

from __future__ import annotations

import datetime as dt
from typing import TYPE_CHECKING

from sqlalchemy import BigInteger
from sqlalchemy import CheckConstraint
from sqlalchemy import ColumnElement
from sqlalchemy import DateTime
from sqlalchemy import ForeignKey
from sqlalchemy import Identity
from sqlalchemy import Index
from sqlalchemy import Text
from sqlalchemy import and_
from sqlalchemy import text
from sqlalchemy.ext.hybrid import hybrid_method
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import mapped_column
from sqlalchemy.orm import relationship

from slate.database.base import Base
from slate.models.initiative import TERMINAL_INITIATIVE_STATUSES
from slate.models.issue import CLOSED_ISSUE_STATUSES
from slate.models.record import Record

if TYPE_CHECKING:
    from slate.models.worklog import Worklog

# How a claim ended. `released` is its holder or a person letting go, `expired` is its lease
# running out first, and `promoted` is an idea becoming its initiative. The rest are the record
# closing under it, named for the closing status it reached.
CLAIM_ENDINGS = ['released', 'expired', 'promoted', *dict.fromkeys([*CLOSED_ISSUE_STATUSES, *TERMINAL_INITIATIVE_STATUSES])]

# The partial unique index that settles two holds racing for one record.
ONE_UNENDED_CLAIM_PER_RECORD = 'uq_claims_one_unended_per_record'

# The partial unique index that lets a claim wait on one thing at a time.
ONE_OPEN_WAIT_PER_CLAIM = 'uq_claim_waits_one_open_per_claim'


class ClaimEnding(Base):
    __tablename__ = 'claim_endings'
    name: Mapped[str] = mapped_column(Text, primary_key=True)


class Claim(Base):
    __tablename__ = 'claims'
    id: Mapped[int] = mapped_column(BigInteger, Identity(), primary_key=True)
    record_number: Mapped[int] = mapped_column(BigInteger, ForeignKey('records.number', ondelete='CASCADE'), nullable=False)
    # Who holds it, as the client names them: `claude-code/<session>`, a service's client id.
    holder: Mapped[str] = mapped_column(Text, nullable=False, index=True)
    # Where the holder works, so a reader can find the session, the box and the checkout.
    session: Mapped[str | None] = mapped_column(Text, ForeignKey('agent_sessions.id'), nullable=True)
    host: Mapped[str | None] = mapped_column(Text, nullable=True)
    directory: Mapped[str | None] = mapped_column(Text, nullable=True)
    started_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))
    expires_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    ended_ts: Mapped[dt.datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    ending: Mapped[str | None] = mapped_column(Text, ForeignKey('claim_endings.name'), nullable=True)
    # Who ended it. Null on an expired claim, which nobody ended.
    ended_by: Mapped[str | None] = mapped_column(Text, nullable=True)

    record: Mapped[Record] = relationship(Record)
    # Every read of a claim names its worklog and whether it waits, so both load with it.
    worklog: Mapped[Worklog | None] = relationship(back_populates='claim', lazy='selectin')
    waits: Mapped[list[ClaimWait]] = relationship(back_populates='claim', lazy='selectin', order_by='(ClaimWait.started_ts, ClaimWait.id)')

    __table_args__ = (
        CheckConstraint('(ended_ts IS NULL) = (ending IS NULL)', name='claim_ending_is_whole'),
        CheckConstraint('ended_by IS NULL OR ended_ts IS NOT NULL', name='claim_ended_by_only_when_ended'),
        # One unended claim per record, so two holders racing for it cannot both insert.
        Index(ONE_UNENDED_CLAIM_PER_RECORD, 'record_number', unique=True, postgresql_where=text('ended_ts IS NULL')),
        Index('idx_claims_record_number', 'record_number'),
    )

    @hybrid_method
    def is_live(self, now: dt.datetime) -> bool:
        """Holding its record at `now`: unended, and its lease not yet run out."""
        return self.ended_ts is None and self.expires_ts > now

    @is_live.expression
    @classmethod
    def _is_live_expression(cls, now: dt.datetime) -> ColumnElement[bool]:
        return and_(cls.ended_ts.is_(None), cls.expires_ts > now)

    @hybrid_method
    def is_expired(self, now: dt.datetime) -> bool:
        """Unended, with its lease run out by `now`, so it no longer holds its record."""
        return self.ended_ts is None and self.expires_ts <= now

    @is_expired.expression
    @classmethod
    def _is_expired_expression(cls, now: dt.datetime) -> ColumnElement[bool]:
        return and_(cls.ended_ts.is_(None), cls.expires_ts <= now)

    def held_until(self, now: dt.datetime) -> dt.datetime:
        """When holding stops, for a claim ended at `now`: its expiry when the lease ran out first, else `now`."""
        return self.expires_ts if self.is_expired(now) else now

    @property
    def open_wait(self) -> ClaimWait | None:
        """What the holder waits on now, or None while it works."""
        return next((wait for wait in self.waits if wait.ended_ts is None), None)

    def __repr__(self):
        return f'Claim(record_number={self.record_number!r}, holder={self.holder!r}, ending={self.ending!r})'


class ClaimWait(Base):
    """A stretch of a claim's life its holder spent waiting on something, such as a merge or another record.

    The claim still holds its record while it waits. The wait ends when its holder resumes, or when
    the claim ends.
    """

    __tablename__ = 'claim_waits'
    id: Mapped[int] = mapped_column(BigInteger, Identity(), primary_key=True)
    claim_id: Mapped[int] = mapped_column(BigInteger, ForeignKey('claims.id', ondelete='CASCADE'), nullable=False)
    waiting_on: Mapped[str] = mapped_column(Text, nullable=False)
    started_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))
    ended_ts: Mapped[dt.datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    # Who ended it: the holder resuming, or whoever ended the claim. Null when the claim expired.
    ended_by: Mapped[str | None] = mapped_column(Text, nullable=True)

    claim: Mapped[Claim] = relationship(back_populates='waits')

    __table_args__ = (
        CheckConstraint('ended_by IS NULL OR ended_ts IS NOT NULL', name='wait_ended_by_only_when_ended'),
        Index(ONE_OPEN_WAIT_PER_CLAIM, 'claim_id', unique=True, postgresql_where=text('ended_ts IS NULL')),
        Index('idx_claim_waits_claim_id', 'claim_id'),
    )
