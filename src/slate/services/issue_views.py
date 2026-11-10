"""Build the issue read shape from rows, readiness and comment counts.

Every endpoint returning issues goes through `issue_views`, so the derived
fields a client sorts and filters on are computed one way. Rows must be loaded
with `ISSUE_LOAD_OPTIONS`, or serializing each one lazy-loads its relationships
in turn.
"""

from __future__ import annotations

import datetime as dt
from collections.abc import Iterable
from dataclasses import dataclass
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
from slate.services.claim_views import unended_claim_views
from slate.services.initiative_waits import waited_on
from slate.services.initiative_waits import waiting_initiatives
from slate.services.issue_readiness import IssueReadiness
from slate.services.issue_readiness import measure_readiness
from slate.services.record_parts import record_parts
from slate.services.record_pursuits import issue_pursuits
from slate.services.record_summaries import record_summaries
from slate.services.record_tags import IssueTags
from slate.services.record_tags import issue_tags

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


@dataclass(frozen=True)
class IssueWaits:
    """What each unclosed issue's initiative waits on, by its id, and by number each issue's waiting initiatives and each blocker."""

    of_initiative: dict[UUID, list[schemas.RecordSummary]]
    on_issue: dict[int, list[schemas.RecordSummary]]
    blocking: dict[int, schemas.RecordSummary]


def issue_waits(session: Session, issues: list[models.Issue], readiness: IssueReadiness) -> IssueWaits:
    """A closed issue waits on nothing through its initiative, so only an unclosed one's initiative is read."""
    return IssueWaits(
        of_initiative=waited_on(session, {issue.initiative_id for issue in issues if issue.initiative_id and not issue.is_closed}),
        on_issue=waiting_initiatives(session, [issue.number for issue in issues]),
        blocking=record_summaries(session, (blocking.number for issue in issues for blocking in readiness.blocked_by.get(issue.id, ()))),
    )


def blockers(issue: models.Issue, readiness: IssueReadiness, waits: IssueWaits) -> list[schemas.Blocker]:
    return [
        schemas.Blocker(**waits.blocking[blocking.number].model_dump(), through_initiative=blocking.through_initiative)
        for blocking in readiness.blocked_by.get(issue.id, ())
    ]


def comment_counts(session: Session, numbers: list[int]) -> dict[int, int]:
    """How many comments sit on each record itself, by number. Line comments count on their documents."""
    if not numbers:
        return {}
    rows = session.execute(
        select(models.Comment.record_number, func.count())
        .where(models.Comment.record_number.in_(numbers), models.Comment.document_id.is_(None))
        .group_by(models.Comment.record_number)
    )
    return dict(rows.all())


def issue_view(
    issue: models.Issue,
    readiness: IssueReadiness,
    comments: dict[int, int],
    claims: dict[int, schemas.Claim],
    waits: IssueWaits,
    tags: IssueTags,
    pursuits: list[schemas.PursuitSummary],
) -> schemas.Issue:
    initiative = issue.initiative
    return schemas.Issue(
        id=issue.id,
        number=issue.number,
        title=issue.title,
        description=issue.description,
        acceptance=issue.acceptance,
        repo=issue.repo,
        paths=issue.paths,
        type=issue.type,
        status=issue.status,
        status_reason=issue.status_reason,
        priority=issue.priority,
        effective_priority=readiness.effective_priority.get(issue.id, issue.priority),
        rank=issue.rank,
        pinned_ts=issue.pinned_ts,
        pinned_by=issue.pinned_by,
        pinned_session=issue.pinned_session,
        deferred_until_date=issue.deferred_until_date,
        claim=claims.get(issue.number),
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
        tags=tags.own,
        effective_tags=tags.effective,
        pursuits=pursuits,
        depends_on=by_number(edge.depends_on for edge in issue.dependencies),
        blocks=by_number(edge.issue for edge in issue.dependents),
        initiative_waits_on=waits.of_initiative.get(issue.initiative_id, []) if issue.initiative_id and not issue.is_closed else [],
        waiting_initiatives=waits.on_issue.get(issue.number, []),
        blocked_by=blockers(issue, readiness, waits),
        child_count=readiness.child_count.get(issue.id, 0),
        open_child_count=readiness.open_child_count.get(issue.id, 0),
        comment_count=comments.get(issue.number, 0),
        is_blocked=issue.id in readiness.blocked,
        is_ready=issue.id in readiness.ready,
        is_autofixable=issue.id in readiness.autofixable,
        created_ts=issue.created_ts,
        updated_ts=issue.updated_ts,
        closed_ts=issue.closed_ts,
        closed_by=issue.closed_by,
    )


def list_order(readiness: IssueReadiness):
    """Unclosed work in queue order; closed work after it, latest first.

    A closed issue's priority says nothing about it any more, and the question a
    list of finished work answers is what finished most recently.
    """

    def key(issue: models.Issue) -> tuple:
        if issue.is_closed:
            closed = issue.closed_ts.timestamp() if issue.closed_ts else float('-inf')
            return (1, -closed, issue.number)
        return (0, readiness.sort_key(issue))

    return key


def issue_views(session: Session, issues: list[models.Issue], zone: str, readiness: IssueReadiness | None = None) -> list[schemas.Issue]:
    today, now = calendar_now(zone)
    if readiness is None:
        readiness = measure_readiness(session, today, now)
    counts = comment_counts(session, [issue.number for issue in issues])
    claims = unended_claim_views(session, [issue.number for issue in issues], now)
    waits = issue_waits(session, issues, readiness)
    tags = issue_tags(session, issues)
    pursuits = issue_pursuits(session, issues)
    return [issue_view(issue, readiness, counts, claims, waits, tags[issue.number], pursuits[issue.number]) for issue in issues]


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
        **record_parts(session, issue.number, dt.datetime.now(dt.UTC)).model_dump(),
        children=[schemas.IssueSummary(id=c.id, number=c.number, title=c.title, status=c.status) for c in children],
    )


def load_issue(session: Session, issue_id: UUID) -> models.Issue:
    return session.scalars(select(models.Issue).options(*ISSUE_LOAD_OPTIONS).where(models.Issue.id == issue_id)).one()


def fresh_issue_detail(session: Session, issue: models.Issue, zone: str) -> schemas.IssueDetail:
    """The issue's detail read again from the database, so a write answers what it committed."""
    session.expire_all()
    return issue_detail(session, load_issue(session, issue.id), zone)
