"""A check: one reading of an issue or an initiative against the world it describes, kept beside every earlier one.

A check names what it found as a verdict from `check_verdicts`, the evidence in prose, and what
that evidence cites. Checks are never edited, so a record's checks read as the history of whether
it still held. The latest one rides on every read of its record, and `services/check_due.py`
orders the records by when they come due for the next.
"""

from __future__ import annotations

import datetime as dt
from typing import Any
from typing import Literal
from typing import get_args

from sqlalchemy import BigInteger
from sqlalchemy import DateTime
from sqlalchemy import ForeignKey
from sqlalchemy import Identity
from sqlalchemy import Index
from sqlalchemy import Text
from sqlalchemy import text
from sqlalchemy.dialects.postgresql import JSONB
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import mapped_column

from slate.database.base import Base

# What a check found, each carrying one move for whoever acts on it. `holds` still describes work to
# do, `corrected` does once what was wrong is rewritten, `done` and `gone` close it, `duplicate`
# names the record carrying the same work, `contradicts` asks for what something else rules out,
# and `unclear` settles nothing, so the record comes due again at its interval.
CheckVerdictName = Literal['holds', 'corrected', 'done', 'gone', 'duplicate', 'contradicts', 'unclear']
CHECK_VERDICTS: list[str] = list(get_args(CheckVerdictName))


class CheckVerdict(Base):
    __tablename__ = 'check_verdicts'
    name: Mapped[str] = mapped_column(Text, primary_key=True)


class RecordCheck(Base):
    __tablename__ = 'record_checks'
    id: Mapped[int] = mapped_column(BigInteger, Identity(), primary_key=True)
    record_number: Mapped[int] = mapped_column(BigInteger, ForeignKey('records.number', ondelete='CASCADE'), nullable=False)
    checked_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))
    # A service's client id, or a person's actor for a check made by hand.
    checked_by: Mapped[str] = mapped_column(Text, nullable=False)
    verdict: Mapped[str] = mapped_column(Text, ForeignKey('check_verdicts.name'), nullable=False)
    evidence: Mapped[str] = mapped_column(Text, nullable=False)
    # Each source the evidence rests on, as `{"kind": ..., "ref": ...}`.
    cites: Mapped[list[dict[str, Any]]] = mapped_column(JSONB, nullable=False, server_default=text("'[]'::jsonb"))
    # The scheduled run that made it, where one did.
    run: Mapped[str | None] = mapped_column(Text, nullable=True)
    session: Mapped[str | None] = mapped_column(Text, ForeignKey('agent_sessions.id'), nullable=True)

    __table_args__ = (Index('idx_record_checks_record_number_checked_ts', 'record_number', 'checked_ts'),)
