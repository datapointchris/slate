from __future__ import annotations

import datetime as dt
from typing import TYPE_CHECKING
from uuid import UUID

from sqlalchemy import Boolean
from sqlalchemy import CheckConstraint
from sqlalchemy import Date
from sqlalchemy import DateTime
from sqlalchemy import ForeignKey
from sqlalchemy import Identity
from sqlalchemy import Index
from sqlalchemy import Integer
from sqlalchemy import SmallInteger
from sqlalchemy import Text
from sqlalchemy import Uuid
from sqlalchemy import text
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import mapped_column
from sqlalchemy.orm import relationship

from slate.database.base import Base
from slate.database.fractionally_ranked import FractionallyRanked
from slate.models.record import Kind
from slate.models.record import NumberedRecord
from slate.models.record import record_link

if TYPE_CHECKING:
    from slate.models.initiative import Initiative

# Whether anyone is working an open issue is its live claim, never a status.
ISSUE_STATUSES = ['triage', 'open', 'completed', 'canceled']
CLOSED_ISSUE_STATUSES = ['completed', 'canceled']

# `decision` is the one type that changes what happens to an issue: it waits on
# a person, so the ready queue an agent reads leaves it out.
ISSUE_TYPES = ['bug', 'feature', 'task', 'chore', 'decision']


class IssueStatus(Base):
    __tablename__ = 'issue_statuses'
    name: Mapped[str] = mapped_column(Text, primary_key=True)


class IssueType(Base):
    __tablename__ = 'issue_types'
    name: Mapped[str] = mapped_column(Text, primary_key=True)


class Issue(NumberedRecord, FractionallyRanked, Base):
    """A unit of software work, written for the agent that will do it.

    `id` is the key and `number` is the handle a person or an agent types. The
    number is its row in `records`, so a bare number names one record of any kind.

    Order is effective priority, then `rank`, then `number`.
    `services/issue_readiness.py` derives the effective priority, and
    `services/issue_rank.py` places `rank`.

    Who is working it is its claim, a row in `claims` on its number.
    """

    RECORD_KIND = Kind.ISSUE

    __tablename__ = 'issues'
    title: Mapped[str] = mapped_column(Text, nullable=False)
    # Facts the agent cannot find in the repo. What the repo already says costs
    # tokens on every read and adds nothing.
    description: Mapped[str | None] = mapped_column(Text, nullable=True)
    # How to tell the work is done. Nothing enforces it; whoever completes the
    # issue checks it.
    acceptance: Mapped[str | None] = mapped_column(Text, nullable=True)
    # Repo registry name. Null is work no single repo owns.
    repo: Mapped[str | None] = mapped_column(Text, nullable=True, index=True)
    type: Mapped[str] = mapped_column(Text, ForeignKey('issue_types.name'), nullable=False, server_default='task')
    status: Mapped[str] = mapped_column(Text, ForeignKey('issue_statuses.name'), nullable=False, server_default='open')
    # Why the issue was canceled. Completed work says what it shipped in a comment.
    status_reason: Mapped[str | None] = mapped_column(Text, nullable=True)
    priority: Mapped[int] = mapped_column(SmallInteger, nullable=False, server_default='0')
    # A calendar day. The issue stays out of the ready queue until the day
    # arrives in the reader's zone, and does not count as blocked meanwhile.
    deferred_until_date: Mapped[dt.date | None] = mapped_column(Date, nullable=True)
    # False keeps the issue from an unattended fix. Its initiative's false does too.
    autofix: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True, server_default=text('true'))
    initiative_id: Mapped[UUID | None] = mapped_column(Uuid, ForeignKey('initiatives.id', ondelete='SET NULL'), nullable=True, index=True)
    # A parent is work too large for one issue, split into children. While any
    # child is open the parent is not ready and cannot be completed.
    parent_id: Mapped[UUID | None] = mapped_column(Uuid, ForeignKey('issues.id', ondelete='SET NULL'), nullable=True, index=True)
    # Provenance only: found while working the other issue, and never gated by it.
    discovered_from_id: Mapped[UUID | None] = mapped_column(Uuid, ForeignKey('issues.id', ondelete='SET NULL'), nullable=True)
    duplicate_of_id: Mapped[UUID | None] = mapped_column(Uuid, ForeignKey('issues.id', ondelete='SET NULL'), nullable=True)
    updated_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))
    # When the issue reached completed or canceled. Null on a closed issue means
    # the time is unknown, which is never inferred from `updated_ts`.
    closed_ts: Mapped[dt.datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    # Who closed it: the actor the client named, else the signed-in user's email.
    # Stamped and cleared with `closed_ts`. Null on a closed issue means unknown.
    closed_by: Mapped[str | None] = mapped_column(Text, nullable=True)

    initiative: Mapped[Initiative | None] = relationship('Initiative', back_populates='issues')
    parent: Mapped[Issue | None] = relationship('Issue', remote_side='Issue.id', foreign_keys=[parent_id], back_populates='children')
    children: Mapped[list[Issue]] = relationship('Issue', foreign_keys=[parent_id], back_populates='parent', order_by='Issue.rank')
    discovered_from: Mapped[Issue | None] = relationship('Issue', remote_side='Issue.id', foreign_keys=[discovered_from_id])
    duplicate_of: Mapped[Issue | None] = relationship('Issue', remote_side='Issue.id', foreign_keys=[duplicate_of_id])
    dependencies: Mapped[list[IssueDependency]] = relationship(
        'IssueDependency', foreign_keys='IssueDependency.issue_id', back_populates='issue', cascade='all, delete-orphan'
    )
    dependents: Mapped[list[IssueDependency]] = relationship(
        'IssueDependency', foreign_keys='IssueDependency.depends_on_id', back_populates='depends_on', cascade='all, delete-orphan'
    )
    label_assignments: Mapped[list[IssueLabelAssignment]] = relationship(
        'IssueLabelAssignment', back_populates='issue', cascade='all, delete-orphan'
    )
    labels: Mapped[list[IssueLabel]] = relationship(
        'IssueLabel', secondary='issue_label_assignments', viewonly=True, order_by='IssueLabel.slug'
    )

    __table_args__ = (
        *record_link(RECORD_KIND),
        CheckConstraint('priority BETWEEN 0 AND 4', name='issue_priority_range'),
        CheckConstraint(
            "status <> 'canceled' OR status_reason IS NOT NULL OR duplicate_of_id IS NOT NULL",
            name='issue_canceled_requires_reason',
        ),
        CheckConstraint("status_reason IS NULL OR status = 'canceled'", name='issue_reason_only_when_canceled'),
        CheckConstraint("closed_ts IS NULL OR status IN ('completed', 'canceled')", name='issue_closed_ts_only_when_closed'),
        CheckConstraint("closed_by IS NULL OR status IN ('completed', 'canceled')", name='issue_closed_by_only_when_closed'),
        CheckConstraint("duplicate_of_id IS NULL OR status = 'canceled'", name='issue_duplicate_only_when_canceled'),
        CheckConstraint('parent_id IS NULL OR parent_id <> id', name='issue_not_own_parent'),
        CheckConstraint('discovered_from_id IS NULL OR discovered_from_id <> id', name='issue_not_discovered_from_itself'),
        CheckConstraint('duplicate_of_id IS NULL OR duplicate_of_id <> id', name='issue_not_duplicate_of_itself'),
        Index('idx_issues_status', 'status'),
        Index('idx_issues_rank', 'rank'),
    )

    @property
    def is_closed(self) -> bool:
        return self.status in CLOSED_ISSUE_STATUSES

    def __repr__(self):
        return f'Issue(number={self.number!r}, title={self.title!r}, status={self.status!r})'


class IssueDependency(Base):
    """`issue_id` is blocked until `depends_on_id` closes.

    A parent's open children also keep it out of the ready queue, but only this
    edge sets `is_blocked`.
    """

    __tablename__ = 'issue_dependencies'
    issue_id: Mapped[UUID] = mapped_column(Uuid, ForeignKey('issues.id', ondelete='CASCADE'), primary_key=True)
    depends_on_id: Mapped[UUID] = mapped_column(Uuid, ForeignKey('issues.id', ondelete='CASCADE'), primary_key=True)

    issue: Mapped[Issue] = relationship('Issue', foreign_keys=[issue_id], back_populates='dependencies')
    depends_on: Mapped[Issue] = relationship('Issue', foreign_keys=[depends_on_id], back_populates='dependents')

    __table_args__ = (
        CheckConstraint('issue_id <> depends_on_id', name='issue_no_self_dependency'),
        Index('idx_issue_dependencies_depends_on', 'depends_on_id'),
    )

    def __repr__(self):
        return f'IssueDependency(issue_id={self.issue_id!r}, depends_on_id={self.depends_on_id!r})'


class IssueLabel(Base):
    """One value in the closed label vocabulary, keyed by its slug.

    A label must exist before an issue can carry it, so a misspelling is refused
    rather than becoming a second label. Labels sharing a `group_slug` exclude
    each other: an issue carries at most one label from each group.
    """

    __tablename__ = 'issue_labels'
    id: Mapped[int] = mapped_column(Integer, Identity(always=True), primary_key=True)
    slug: Mapped[str] = mapped_column(Text, nullable=False, unique=True)
    group_slug: Mapped[str | None] = mapped_column(Text, nullable=True)
    description: Mapped[str | None] = mapped_column(Text, nullable=True)

    __table_args__ = (
        CheckConstraint("slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'", name='issue_label_slug_shape'),
        CheckConstraint("group_slug IS NULL OR group_slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'", name='issue_label_group_slug_shape'),
    )

    def __repr__(self):
        return f'IssueLabel(slug={self.slug!r}, group_slug={self.group_slug!r})'


class IssueLabelAssignment(Base):
    __tablename__ = 'issue_label_assignments'
    issue_id: Mapped[UUID] = mapped_column(Uuid, ForeignKey('issues.id', ondelete='CASCADE'), primary_key=True)
    label_id: Mapped[int] = mapped_column(Integer, ForeignKey('issue_labels.id', ondelete='CASCADE'), primary_key=True)

    issue: Mapped[Issue] = relationship('Issue', back_populates='label_assignments')
    label: Mapped[IssueLabel] = relationship('IssueLabel')

    __table_args__ = (Index('idx_issue_label_assignments_label', 'label_id'),)
