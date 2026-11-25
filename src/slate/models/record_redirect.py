"""A retired record number, leading to the record that took its rows, so the number still resolves.

A fold ends an idea into the work that covers it: its rows move to the target, its `records` row
goes, and this row leads its number there. The number came from the sequence and is never handed
out again, so it keys the redirect. `former_*` keep what a reader of the old number needs once the
idea's row is gone. A fold retargets every redirect that led to the folded idea, so a redirect
always leads one step, as Jira's `moved_issue_key` does. Deleting the target deletes the redirect,
and the number then answers 404, as the target does.
"""

from __future__ import annotations

import datetime as dt
from typing import Literal
from typing import get_args
from uuid import UUID

from sqlalchemy import BigInteger
from sqlalchemy import CheckConstraint
from sqlalchemy import DateTime
from sqlalchemy import ForeignKey
from sqlalchemy import Index
from sqlalchemy import SmallInteger
from sqlalchemy import Text
from sqlalchemy import Uuid
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import mapped_column

from slate.database.base import Base

# Why the number leads elsewhere: `folded` into work that covers it, or `promoted` into the initiative
# that an idea became before a promotion kept the idea's number.
RedirectReason = Literal['folded', 'promoted']
REDIRECT_REASONS: list[str] = list(get_args(RedirectReason))
FOLDED: RedirectReason = 'folded'


class RecordRedirect(Base):
    __tablename__ = 'record_redirects'
    number: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=False)
    to_number: Mapped[int] = mapped_column(BigInteger, ForeignKey('records.number', ondelete='CASCADE'), nullable=False)
    reason: Mapped[str] = mapped_column(Text, nullable=False)
    former_kind: Mapped[str] = mapped_column(Text, ForeignKey('record_kinds.name'), nullable=False)
    # The kind row's id, so a lookup by the idea's UUID follows the redirect too.
    former_id: Mapped[UUID] = mapped_column(Uuid, nullable=False, unique=True)
    former_title: Mapped[str] = mapped_column(Text, nullable=False)
    former_priority: Mapped[int] = mapped_column(SmallInteger, nullable=False)
    former_flow_id: Mapped[str | None] = mapped_column(Text, nullable=True)
    former_created_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    redirected_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    redirected_by: Mapped[str] = mapped_column(Text, nullable=False)

    __table_args__ = (
        CheckConstraint(f'reason IN ({", ".join(repr(reason) for reason in REDIRECT_REASONS)})', name='redirect_reason_is_known'),
        CheckConstraint('number <> to_number', name='redirect_leads_elsewhere'),
        Index('idx_record_redirects_to_number', 'to_number'),
    )

    def __repr__(self) -> str:
        return f'RecordRedirect(number={self.number!r}, to_number={self.to_number!r}, reason={self.reason!r})'
