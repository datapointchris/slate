"""A worklog is one go at one record: the sessions that joined it, what it produced, and how it ended.

A worklog is named under its record's number, so `1991.2` is the second worklog on 1991. Holding a
claim opens one, and `worklogs start` opens one with no claim. The claim ending, the record closing
or `worklogs finish` ends it, and `outcome` says which.

A session joins a worklog for a span of time. A session is in at most one worklog at a time, so its
spans never overlap, and its time outside every span is its own. `services/worklogs.py` holds every
write, and `services/worklog_views.py` derives each worklog's state and stage on read.
"""

from __future__ import annotations

import datetime as dt
from typing import Literal
from typing import get_args

from sqlalchemy import BigInteger
from sqlalchemy import CheckConstraint
from sqlalchemy import DateTime
from sqlalchemy import ForeignKey
from sqlalchemy import Identity
from sqlalchemy import Index
from sqlalchemy import Integer
from sqlalchemy import Text
from sqlalchemy import UniqueConstraint
from sqlalchemy import text
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import mapped_column
from sqlalchemy.orm import relationship

from slate.database.base import Base
from slate.models.claim import CLAIM_ENDINGS
from slate.models.claim import Claim
from slate.models.record import Record

# `finished` is `worklogs finish` ending it by hand. Every other outcome is the ending of the claim
# that opened it, or the closing status its record reached.
WORKLOG_FINISHED = 'finished'
WORKLOG_OUTCOMES = [WORKLOG_FINISHED, *CLAIM_ENDINGS]

# How the work was approached, kept so later work can compare the approaches.
WorkflowName = Literal['oneshot', 'plan-first', 'design-first']
WORKLOG_WORKFLOWS: list[str] = list(get_args(WorkflowName))

# What a worklog produced, in the order a change reaches main. A landing is a commit or a pull
# request reaching the main branch.
LinkKindName = Literal['commit', 'pull_request', 'landing']
WORKLOG_LINK_KINDS: list[str] = list(get_args(LinkKindName))

# The partial unique index that keeps a session in one worklog at a time.
ONE_OPEN_SPAN_PER_SESSION = 'uq_worklog_spans_one_open_per_session'


class WorklogOutcome(Base):
    __tablename__ = 'worklog_outcomes'
    name: Mapped[str] = mapped_column(Text, primary_key=True)


class WorklogWorkflow(Base):
    __tablename__ = 'worklog_workflows'
    name: Mapped[str] = mapped_column(Text, primary_key=True)


class WorklogLinkKind(Base):
    __tablename__ = 'worklog_link_kinds'
    name: Mapped[str] = mapped_column(Text, primary_key=True)


class Worklog(Base):
    __tablename__ = 'worklogs'
    id: Mapped[int] = mapped_column(BigInteger, Identity(), primary_key=True)
    record_number: Mapped[int] = mapped_column(BigInteger, ForeignKey('records.number', ondelete='CASCADE'), nullable=False)
    # The nth worklog on its record, counting from one.
    sequence: Mapped[int] = mapped_column(Integer, nullable=False)
    # The claim whose hold opened it. Null on one `worklogs start` opened.
    claim_id: Mapped[int | None] = mapped_column(BigInteger, ForeignKey('claims.id', ondelete='CASCADE'), nullable=True, unique=True)
    opened_by: Mapped[str] = mapped_column(Text, nullable=False)
    workflow: Mapped[str | None] = mapped_column(Text, ForeignKey('worklog_workflows.name'), nullable=True)
    started_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))
    ended_ts: Mapped[dt.datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    outcome: Mapped[str | None] = mapped_column(Text, ForeignKey('worklog_outcomes.name'), nullable=True)
    # Who ended it. Null when its claim expired, which nobody ended.
    ended_by: Mapped[str | None] = mapped_column(Text, nullable=True)

    record: Mapped[Record] = relationship(Record)
    claim: Mapped[Claim | None] = relationship(Claim, back_populates='worklog')
    spans: Mapped[list[WorklogSpan]] = relationship(back_populates='worklog', order_by='(WorklogSpan.started_ts, WorklogSpan.id)')
    links: Mapped[list[WorklogLink]] = relationship(back_populates='worklog', order_by='(WorklogLink.linked_ts, WorklogLink.id)')

    __table_args__ = (
        UniqueConstraint('record_number', 'sequence', name='uq_worklogs_one_per_sequence_on_a_record'),
        CheckConstraint('sequence >= 1', name='sequence_counts_from_one'),
        CheckConstraint('(ended_ts IS NULL) = (outcome IS NULL)', name='worklog_outcome_is_whole'),
        CheckConstraint('ended_by IS NULL OR ended_ts IS NOT NULL', name='worklog_ended_by_only_when_ended'),
    )

    @property
    def name(self) -> str:
        return f'{self.record_number}.{self.sequence}'

    def __repr__(self) -> str:
        return f'Worklog(name={self.name!r}, outcome={self.outcome!r})'


class WorklogSpan(Base):
    """One session's time in one worklog, from joining it until it left or the worklog ended."""

    __tablename__ = 'worklog_spans'
    id: Mapped[int] = mapped_column(BigInteger, Identity(), primary_key=True)
    worklog_id: Mapped[int] = mapped_column(BigInteger, ForeignKey('worklogs.id', ondelete='CASCADE'), nullable=False)
    session_id: Mapped[str] = mapped_column(Text, ForeignKey('agent_sessions.id'), nullable=False)
    # Where the session worked when it joined.
    directory: Mapped[str | None] = mapped_column(Text, nullable=True)
    started_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))
    ended_ts: Mapped[dt.datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)

    worklog: Mapped[Worklog] = relationship(back_populates='spans')

    __table_args__ = (
        CheckConstraint('ended_ts IS NULL OR ended_ts >= started_ts', name='span_ends_after_it_starts'),
        Index(ONE_OPEN_SPAN_PER_SESSION, 'session_id', unique=True, postgresql_where=text('ended_ts IS NULL')),
        Index('idx_worklog_spans_session_id', 'session_id'),
        Index('idx_worklog_spans_worklog_id', 'worklog_id'),
    )


class WorklogLink(Base):
    """A commit, pull request or landing a worklog produced, stored as given and never followed."""

    __tablename__ = 'worklog_links'
    id: Mapped[int] = mapped_column(BigInteger, Identity(), primary_key=True)
    worklog_id: Mapped[int] = mapped_column(BigInteger, ForeignKey('worklogs.id', ondelete='CASCADE'), nullable=False)
    kind: Mapped[str] = mapped_column(Text, ForeignKey('worklog_link_kinds.name'), nullable=False)
    # A commit's sha, or a pull request's URL.
    ref: Mapped[str] = mapped_column(Text, nullable=False)
    repo: Mapped[str | None] = mapped_column(Text, nullable=True)
    # The session it came from, when one made it.
    session_id: Mapped[str | None] = mapped_column(Text, ForeignKey('agent_sessions.id'), nullable=True)
    linked_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))
    linked_by: Mapped[str] = mapped_column(Text, nullable=False)

    worklog: Mapped[Worklog] = relationship(back_populates='links')

    __table_args__ = (UniqueConstraint('worklog_id', 'kind', 'ref', name='uq_worklog_links_one_per_ref'),)
