"""Build the issue read shape from rows, readiness and comment counts.

Every endpoint returning issues goes through `issue_views`, so the derived
fields a client sorts and filters on are computed one way. Rows must be loaded
with `ISSUE_LOAD_OPTIONS`, or serializing each one lazy-loads its relationships
in turn.
"""

from __future__ import annotations

import datetime as dt
from collections.abc import Iterable
from uuid import UUID
from zoneinfo import ZoneInfo

from sqlalchemy import and_
from sqlalchemy import func
from sqlalchemy import select
from sqlalchemy.orm import Session
from sqlalchemy.orm import selectinload

from slate import models
from slate import schemas
from slate.models.issue import CLOSED_ISSUE_STATUSES
from slate.services.issue_readiness import IssueReadiness
from slate.services.issue_readiness import measure_readiness
from slate.services.issue_readiness import urgency

ISSUE_LOAD_OPTIONS = (
    selectinload(models.Issue.initiative),
    selectinload(models.Issue.parent),
    selectinload(models.Issue.discovered_from),
    selectinload(models.Issue.duplicate_of),
    selectinload(models.Issue.labels),
    selectinload(models.Issue.dependencies).selectinload(models.IssueDependency.depends_on),
    selectinload(models.Issue.dependents).selectinload(models.IssueDependency.issue),
)


def calendar_now(zone: str) -> tuple[dt.date, dt.datetime]:
    """Today in the reader's zone, and the instant it was read at."""
    now = dt.datetime.now(dt.UTC)
    return now.astimezone(ZoneInfo(zone)).date(), now


def summary(issue: models.Issue | None) -> schemas.IssueSummary | None:
    if issue is None:
        return None
    return schemas.IssueSummary(id=issue.id, number=issue.number, title=issue.title, status=issue.status)


def by_number(issues: Iterable[models.Issue]) -> list[schemas.IssueSummary]:
    return [schemas.IssueSummary(id=i.id, number=i.number, title=i.title, status=i.status) for i in sorted(issues, key=lambda i: i.number)]


def comment_counts(session: Session, issue_ids: list[UUID]) -> dict[UUID, int]:
    if not issue_ids:
        return {}
    rows = session.execute(
        select(models.IssueComment.issue_id, func.count())
        .where(models.IssueComment.issue_id.in_(issue_ids))
        .group_by(models.IssueComment.issue_id)
    )
    return dict(rows.all())


def issue_view(issue: models.Issue, readiness: IssueReadiness, comments: dict[UUID, int]) -> schemas.Issue:
    initiative = issue.initiative
    return schemas.Issue(
        id=issue.id,
        number=issue.number,
        title=issue.title,
        description=issue.description,
        acceptance=issue.acceptance,
        repo=issue.repo,
        type=issue.type,
        status=issue.status,
        status_reason=issue.status_reason,
        priority=issue.priority,
        effective_priority=readiness.effective_priority.get(issue.id, issue.priority),
        rank=issue.rank,
        deferred_until_date=issue.deferred_until_date,
        claimed_by=issue.claimed_by,
        claim_expires_ts=issue.claim_expires_ts,
        autofix=issue.autofix,
        initiative=None
        if initiative is None
        else schemas.InitiativeSummary(
            id=initiative.id,
            number=initiative.number,
            name=initiative.name,
            status=initiative.status,
            priority=initiative.priority,
            autofix=initiative.autofix,
        ),
        parent=summary(issue.parent),
        discovered_from=summary(issue.discovered_from),
        duplicate_of=summary(issue.duplicate_of),
        labels=[label.slug for label in issue.labels],
        depends_on=by_number(edge.depends_on for edge in issue.dependencies),
        blocks=by_number(edge.issue for edge in issue.dependents),
        child_count=readiness.child_count.get(issue.id, 0),
        open_child_count=readiness.open_child_count.get(issue.id, 0),
        comment_count=comments.get(issue.id, 0),
        is_blocked=issue.id in readiness.blocked,
        is_ready=issue.id in readiness.ready,
        is_autofixable=issue.id in readiness.autofixable,
        created_ts=issue.created_ts,
        updated_ts=issue.updated_ts,
        closed_ts=issue.closed_ts,
        closed_by=issue.closed_by,
    )


def list_order(readiness: IssueReadiness):
    """Open work by effective priority then rank; closed work after it, latest first.

    A closed issue's priority says nothing about it any more, and the question a
    list of finished work answers is what finished most recently.
    """

    def key(issue: models.Issue):
        if issue.is_closed:
            closed = issue.closed_ts.timestamp() if issue.closed_ts else float('-inf')
            return (1, 0, -closed, issue.number)
        return (0, urgency(readiness.effective_priority.get(issue.id, issue.priority)), issue.rank, issue.number)

    return key


def issue_views(session: Session, issues: list[models.Issue], zone: str, readiness: IssueReadiness | None = None) -> list[schemas.Issue]:
    if readiness is None:
        today, now = calendar_now(zone)
        readiness = measure_readiness(session, today, now)
    counts = comment_counts(session, [issue.id for issue in issues])
    return [issue_view(issue, readiness, counts) for issue in issues]


def label_views(session: Session, slug: str | None = None) -> list[schemas.IssueLabel]:
    """Every label, or the one named, with how many unclosed issues carry it."""
    open_assignment = and_(
        models.IssueLabelAssignment.label_id == models.IssueLabel.id,
        models.IssueLabelAssignment.issue_id.in_(select(models.Issue.id).where(models.Issue.status.not_in(CLOSED_ISSUE_STATUSES))),
    )
    query = (
        select(models.IssueLabel, func.count(models.IssueLabelAssignment.issue_id))
        .outerjoin(models.IssueLabelAssignment, open_assignment)
        .group_by(models.IssueLabel.id)
        .order_by(models.IssueLabel.group_slug.asc().nullslast(), models.IssueLabel.slug)
    )
    if slug is not None:
        query = query.where(models.IssueLabel.slug == slug)
    return [
        schemas.IssueLabel(slug=label.slug, group_slug=label.group_slug, description=label.description, open_issue_count=count)
        for label, count in session.execute(query).all()
    ]


def issue_detail(session: Session, issue: models.Issue, zone: str) -> schemas.IssueDetail:
    view = issue_views(session, [issue], zone)[0]
    children = session.scalars(select(models.Issue).where(models.Issue.parent_id == issue.id).order_by(models.Issue.rank)).all()
    return schemas.IssueDetail(
        **view.model_dump(),
        children=[schemas.IssueSummary(id=c.id, number=c.number, title=c.title, status=c.status) for c in children],
        comments=[schemas.IssueComment.model_validate(comment) for comment in issue.comments],
    )
