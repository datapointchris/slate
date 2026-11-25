"""Build the idea read shape, and the list of every number that is or was an idea with what each became.

An open idea reads from its `ideas` row. A promotion kept the idea's number and `id` on the record
it turned into, so that record reads as the idea it was, from its own rows. A number a fold
retired, or an idea promoted before promotion kept the number, reads from its redirect, and names
the record it leads to. Such a pair's initiative never was an idea, so it is not listed.
"""

from __future__ import annotations

import datetime as dt
from collections.abc import Sequence

from sqlalchemy import ColumnElement
from sqlalchemy import Select
from sqlalchemy import case
from sqlalchemy import literal
from sqlalchemy import select
from sqlalchemy import union_all
from sqlalchemy.orm import Session
from sqlalchemy.sql.elements import SQLCoreOperations

from slate import models
from slate import schemas
from slate.models.record import Kind
from slate.models.record_redirect import PROMOTED
from slate.schemas.idea import IdeaStateFilter
from slate.schemas.ready_filter import RecordGroupings
from slate.services.claim_views import unended_claim_views
from slate.services.idea_promotion import PromotedIdea
from slate.services.initiative_views import counted_initiatives
from slate.services.issue_readiness import NO_PRIORITY_URGENCY
from slate.services.record_groupings import narrowed_to_groupings
from slate.services.record_parts import record_parts
from slate.services.record_pursuits import pursuits_of
from slate.services.record_refs import refs_of
from slate.services.record_summaries import record_summaries
from slate.services.record_tags import own_tags
from slate.services.row_limit import apply_row_limit

# Where a listed number reads from: its `ideas` row, its kind row after a promotion, or its redirect.
REDIRECT = 'redirect'

# An idea folds into an idea, an initiative or an issue, so a retired idea's redirect leads to one of these.
FOLD_TARGETS = (Kind.IDEA, Kind.INITIATIVE, Kind.ISSUE)


def idea_views(session: Session, ideas: Sequence[models.Idea]) -> list[schemas.Idea]:
    claims = unended_claim_views(session, [idea.number for idea in ideas], dt.datetime.now(dt.UTC))
    tags = own_tags(session, [idea.number for idea in ideas])
    pursuits = pursuits_of(session, [idea.number for idea in ideas])
    refs = refs_of(session, [idea.number for idea in ideas])
    return [
        schemas.Idea(
            id=idea.id,
            number=idea.number,
            title=idea.title,
            description=idea.description,
            priority=idea.priority,
            rank=idea.rank,
            pinned_ts=idea.pinned_ts,
            pinned_by=idea.pinned_by,
            pinned_session=idea.pinned_session,
            flow_id=idea.flow_id,
            created_ts=idea.created_ts,
            updated_ts=idea.updated_ts,
            tags=tags.get(idea.number, []),
            pursuits=pursuits.get(idea.number, []),
            refs=refs.get(idea.number, []),
            claim=claims.get(idea.number),
        )
        for idea in ideas
    ]


def idea_view(session: Session, idea: models.Idea) -> schemas.Idea:
    return idea_views(session, [idea])[0]


def urgency(priority: SQLCoreOperations[int]) -> ColumnElement[int]:
    """Priority 0 is no priority, so it sorts after low."""
    return case((priority == 0, NO_PRIORITY_URGENCY), else_=priority)


def promoted_in_place(model: type[models.Initiative | models.Issue], kind: Kind) -> Select:
    """Each record of `kind` that was promoted in place from an idea, with its urgency.

    A pair's initiative is marked promoted when it was created, and never was an idea, so the
    initiative a `promoted` redirect leads to is left out.
    """
    pairs = select(models.RecordRedirect.to_number).where(models.RecordRedirect.reason == PROMOTED)
    return (
        select(model.number, literal(kind.value).label('source'), urgency(model.priority).label('urgency'))
        .join(models.Record, models.Record.number == model.number)
        .where(models.Record.promoted_ts.is_not(None), model.number.not_in(pairs))
    )


def retired_into(kind: Kind) -> Select:
    """Each number retired from an idea whose redirect leads to a record of `kind`, with its urgency as an idea."""
    target = models.KIND_MODELS[kind]
    return (
        select(
            models.RecordRedirect.number, literal(REDIRECT).label('source'), urgency(models.RecordRedirect.former_priority).label('urgency')
        )
        .join(target, target.number == models.RecordRedirect.to_number)
        .where(models.RecordRedirect.former_kind == Kind.IDEA)
    )


def listed_ideas(session: Session, groupings: RecordGroupings, state: IdeaStateFilter, limit: int | None) -> list[schemas.Idea]:
    """The ideas in `state`, most urgent first, then by number, the first `limit` of them.

    A tag, a pursuit or a ref narrows an open idea or a promoted record by its own. A retired
    number's rows moved to its redirect's target, so it narrows by the target's.
    """
    sources = []
    if state in ('open', 'all'):
        idea = select(models.Idea.number, literal(Kind.IDEA.value).label('source'), urgency(models.Idea.priority).label('urgency'))
        sources.append(narrowed_to_groupings(idea, Kind.IDEA, groupings, session))
    if state in ('ended', 'all'):
        sources += [
            narrowed_to_groupings(promoted_in_place(models.Initiative, Kind.INITIATIVE), Kind.INITIATIVE, groupings, session),
            narrowed_to_groupings(promoted_in_place(models.Issue, Kind.ISSUE), Kind.ISSUE, groupings, session),
            *(narrowed_to_groupings(retired_into(kind), kind, groupings, session) for kind in FOLD_TARGETS),
        ]
    items = union_all(*sources).subquery()
    query = select(items.c.number, items.c.source).order_by(items.c.urgency, items.c.number)
    listed = session.execute(apply_row_limit(query, limit)).all()
    by_source: dict[str, list[int]] = {}
    for number, source in listed:
        by_source.setdefault(source, []).append(number)
    views: dict[int, schemas.Idea] = {}
    if open_numbers := by_source.get(Kind.IDEA.value):
        ideas = session.scalars(select(models.Idea).where(models.Idea.number.in_(open_numbers))).all()
        views |= {view.number: view for view in idea_views(session, ideas)}
    if numbers := by_source.get(Kind.INITIATIVE.value):
        views |= {view.number: view for view in promoted_initiative_views(session, numbers)}
    if numbers := by_source.get(Kind.ISSUE.value):
        views |= {view.number: view for view in promoted_issue_views(session, numbers)}
    if numbers := by_source.get(REDIRECT):
        views |= {view.number: view for view in retired_idea_views(session, numbers)}
    return [views[number] for number, _ in listed]


def promotion_moments(session: Session, numbers: list[int]) -> dict[int, dt.datetime]:
    query = select(models.Record.number, models.Record.promoted_ts).where(models.Record.number.in_(numbers))
    return {number: promoted_ts for number, promoted_ts in session.execute(query) if promoted_ts is not None}


def promoted_initiative_views(session: Session, numbers: list[int]) -> list[schemas.Idea]:
    """Each initiative that was promoted in place from an idea, read as the idea it was."""
    initiatives = session.scalars(select(models.Initiative).where(models.Initiative.number.in_(numbers))).all()
    promoted = promotion_moments(session, numbers)
    return [
        schemas.Idea(
            id=initiative.id,
            number=initiative.number,
            title=initiative.name,
            description=initiative.description,
            priority=initiative.priority,
            rank=initiative.rank,
            pinned_ts=initiative.pinned_ts,
            pinned_by=initiative.pinned_by,
            pinned_session=initiative.pinned_session,
            flow_id=initiative.flow_id,
            created_ts=initiative.created_ts,
            updated_ts=promoted[initiative.number],
            tags=initiative.tags,
            pursuits=initiative.pursuits,
            refs=initiative.refs,
            became=schemas.Became(
                how=PROMOTED,
                kind=Kind.INITIATIVE,
                number=initiative.number,
                at=promoted[initiative.number],
                title=initiative.name,
                status=initiative.status,
            ),
            initiative=initiative,
            claim=initiative.claim,
        )
        for initiative in counted_initiatives(session, initiatives)
    ]


def promoted_issue_views(session: Session, numbers: list[int]) -> list[schemas.Idea]:
    """Each issue that was promoted in place from an idea, read as the idea it was."""
    issues = session.scalars(select(models.Issue).where(models.Issue.number.in_(numbers))).all()
    promoted = promotion_moments(session, numbers)
    claims = unended_claim_views(session, numbers, dt.datetime.now(dt.UTC))
    tags = own_tags(session, numbers)
    refs = refs_of(session, numbers)
    return [
        schemas.Idea(
            id=issue.id,
            number=issue.number,
            title=issue.title,
            description=issue.description,
            priority=issue.priority,
            rank=issue.rank,
            pinned_ts=issue.pinned_ts,
            pinned_by=issue.pinned_by,
            pinned_session=issue.pinned_session,
            flow_id=issue.flow_id,
            created_ts=issue.created_ts,
            updated_ts=issue.updated_ts,
            tags=tags.get(issue.number, []),
            refs=refs.get(issue.number, []),
            became=schemas.Became(
                how=PROMOTED, kind=Kind.ISSUE, number=issue.number, at=promoted[issue.number], title=issue.title, status=issue.status
            ),
            claim=claims.get(issue.number),
        )
        for issue in issues
    ]


def retired_idea_views(session: Session, numbers: list[int]) -> list[schemas.Idea]:
    """Each idea number a fold or a pair's migration retired, read from its redirect, naming the record it leads to as it stands."""
    redirects = session.scalars(select(models.RecordRedirect).where(models.RecordRedirect.number.in_(numbers))).all()
    targets = record_summaries(session, [redirect.to_number for redirect in redirects])
    initiative_targets = [redirect.to_number for redirect in redirects if targets[redirect.to_number].kind == Kind.INITIATIVE]
    initiatives = {
        view.number: view
        for view in counted_initiatives(
            session, session.scalars(select(models.Initiative).where(models.Initiative.number.in_(initiative_targets))).all()
        )
    }
    return [
        schemas.Idea(
            id=redirect.former_id,
            number=redirect.number,
            title=redirect.former_title,
            priority=redirect.former_priority,
            flow_id=redirect.former_flow_id,
            created_ts=redirect.former_created_ts,
            updated_ts=redirect.redirected_ts,
            became=schemas.Became(
                how=redirect.reason,
                kind=targets[redirect.to_number].kind,
                number=redirect.to_number,
                at=redirect.redirected_ts,
                title=targets[redirect.to_number].title,
                status=targets[redirect.to_number].status,
            ),
            initiative=initiatives.get(redirect.to_number),
        )
        for redirect in redirects
    ]


def promoted_idea_view(promoted: PromotedIdea, initiative: schemas.Initiative, now: dt.datetime) -> schemas.Idea:
    """The idea in the shape that CLI releases through `cli/v0.66.0` read from a promotion, naming the initiative it became.

    The initiative holds the number's parts, so the view reads them from it.
    """
    return schemas.Idea(
        id=promoted.id,
        number=initiative.number,
        title=promoted.title,
        description=promoted.description,
        priority=promoted.priority,
        rank=initiative.rank,
        pinned_ts=initiative.pinned_ts,
        pinned_by=initiative.pinned_by,
        pinned_session=initiative.pinned_session,
        flow_id=initiative.flow_id,
        created_ts=promoted.created_ts,
        updated_ts=now,
        tags=initiative.tags,
        pursuits=initiative.pursuits,
        refs=initiative.refs,
        became=schemas.Became(
            how=PROMOTED, kind=Kind.INITIATIVE, number=initiative.number, at=now, title=initiative.name, status=initiative.status
        ),
        initiative=initiative,
    )


def idea_detail(session: Session, idea: models.Idea) -> schemas.IdeaDetail:
    return schemas.IdeaDetail(
        **idea_view(session, idea).model_dump(),
        **record_parts(session, idea.number, dt.datetime.now(dt.UTC)).model_dump(),
    )
