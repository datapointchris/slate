"""A name a worklog held before a fold renumbered it under another record, so the old name still reads it.

A worklog's name is its record's number and its sequence there, so moving it to another record
renames it. A commit's `Worklog:` trailer and every log line keep the old name, so each old name
maps to the worklog itself rather than to a newer name, and no chain forms when it moves again.
"""

from __future__ import annotations

import datetime as dt

from sqlalchemy import BigInteger
from sqlalchemy import DateTime
from sqlalchemy import ForeignKey
from sqlalchemy import Index
from sqlalchemy import Text
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import mapped_column

from slate.database.base import Base


class WorklogRedirect(Base):
    __tablename__ = 'worklog_redirects'
    # The former name, as `4086.1`.
    name: Mapped[str] = mapped_column(Text, primary_key=True)
    worklog_id: Mapped[int] = mapped_column(BigInteger, ForeignKey('worklogs.id', ondelete='CASCADE'), nullable=False)
    redirected_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False)

    __table_args__ = (Index('idx_worklog_redirects_worklog_id', 'worklog_id'),)

    def __repr__(self) -> str:
        return f'WorklogRedirect(name={self.name!r}, worklog_id={self.worklog_id!r})'
