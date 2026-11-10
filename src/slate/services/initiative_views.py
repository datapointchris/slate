"""Build the initiative read shape: the initiative, the counts of its issues, its claim, its idea and its checked steps."""

from __future__ import annotations

import datetime as dt
from collections.abc import Sequence
from typing import Any
from uuid import UUID

from sqlalchemy import distinct
from sqlalchemy import func
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.issue import CLOSED_ISSUE_STATUSES
from slate.services.claim_views import unended_claim_views
from slate.services.initiative_waits import waited_on
from slate.services.initiative_waits import waiting_initiatives
from slate.services.record_parts import record_parts
from slate.services.record_steps import initiative_steps
from slate.services.record_steps import stage_of
from slate.services.record_tags import own_tags


def issue_count_columns():
    """Every count in one pass over the join, partitioned by whether the issue is closed and how."""
    unclosed = models.Issue.status.not_in(CLOSED_ISSUE_STATUSES)
    return (
        func.count(models.Issue.id).label('issue_count'),
        func.count(models.Issue.id).filter(unclosed).label('open_count'),
        func.count(models.Issue.id).filter(models.Issue.status == 'completed').label('completed_count'),
        func.count(models.Issue.id).filter(models.Issue.status == 'canceled').label('canceled_count'),
        func.array_agg(distinct(models.Issue.repo))
        .filter(models.Issue.status != 'canceled', models.Issue.repo.is_not(None))
        .label('repos'),
    )


def initiative_view(
    initiative: models.Initiative,
    counts: Sequence[Any],
    claim: schemas.Claim | None,
    idea: schemas.RecordSummary | None,
    depends_on: list[schemas.RecordSummary],
    waiting: list[schemas.RecordSummary],
    steps: list[schemas.CheckedStep],
    tags: list[str],
) -> schemas.Initiative:
    """`counts` is a row of `issue_count_columns()`."""
    issue_count, open_count, completed_count, canceled_count, repos = counts
    return schemas.Initiative(
        id=initiative.id,
        number=initiative.number,
        name=initiative.name,
        summary=initiative.summary,
        description=initiative.description,
        status=initiative.status,
        status_reason=initiative.status_reason,
        priority=initiative.priority,
        rank=initiative.rank,
        pinned_ts=initiative.pinned_ts,
        pinned_by=initiative.pinned_by,
        pinned_session=initiative.pinned_session,
        autofix=initiative.autofix,
        created_ts=initiative.created_ts,
        closed_ts=initiative.closed_ts,
        tags=tags,
        issue_count=issue_count,
        open_count=open_count,
        completed_count=completed_count,
        canceled_count=canceled_count,
        repos=sorted(repos) if repos else [],
        claim=claim,
        idea=idea,
        depends_on=depends_on,
        waiting_initiatives=waiting,
        steps=steps,
        stage=stage_of(steps),
    )


def initiative_views(session: Session, rows: Sequence[tuple[models.Initiative, Sequence[Any]]]) -> list[schemas.Initiative]:
    """Each initiative beside its row of `issue_count_columns()`, with its claim, the idea it came from and its waits either way."""
    claims = unended_claim_views(session, [initiative.number for initiative, _ in rows], dt.datetime.now(dt.UTC))
    depends_on = waited_on(session, [initiative.id for initiative, _ in rows])
    waiting = waiting_initiatives(session, [initiative.number for initiative, _ in rows])
    steps = initiative_steps(session, [initiative for initiative, _ in rows])
    tags = own_tags(session, [initiative.number for initiative, _ in rows])
    idea_ids = [initiative.idea_id for initiative, _ in rows if initiative.idea_id is not None]
    ideas = {
        idea_id: schemas.RecordSummary(number=number, kind=models.Idea.RECORD_KIND, title=title)
        for idea_id, number, title in session.execute(
            select(models.Idea.id, models.Idea.number, models.Idea.title).where(models.Idea.id.in_(idea_ids))
        ).all()
    }
    return [
        initiative_view(
            initiative,
            counts,
            claims.get(initiative.number),
            ideas.get(initiative.idea_id) if initiative.idea_id else None,
            depends_on.get(initiative.id, []),
            waiting.get(initiative.number, []),
            steps.get(initiative.number, []),
            tags.get(initiative.number, []),
        )
        for initiative, counts in rows
    ]


def counted(session: Session, initiative: models.Initiative) -> schemas.Initiative:
    counts = session.execute(select(*issue_count_columns()).where(models.Issue.initiative_id == initiative.id)).one()
    return initiative_views(session, [(initiative, counts)])[0]


def counted_initiatives(session: Session, initiatives: Sequence[models.Initiative]) -> list[schemas.Initiative]:
    """Each initiative counted, in the order given, in one query for all of them."""
    query = (
        select(models.Initiative.id, *issue_count_columns())
        .outerjoin(models.Issue, models.Issue.initiative_id == models.Initiative.id)
        .where(models.Initiative.id.in_([initiative.id for initiative in initiatives]))
        .group_by(models.Initiative.id)
    )
    counts = {row[0]: row[1:] for row in session.execute(query).all()}
    return initiative_views(session, [(initiative, counts[initiative.id]) for initiative in initiatives])


def initiative_detail(session: Session, initiative: models.Initiative) -> schemas.InitiativeDetail:
    """The initiative counted, with every claim, comment and document it has, as an idea's and an issue's reads carry theirs."""
    return schemas.InitiativeDetail(
        **counted(session, initiative).model_dump(),
        **record_parts(session, initiative.number, dt.datetime.now(dt.UTC)).model_dump(),
    )


def initiatives_of_ideas(session: Session, idea_ids: Sequence[UUID]) -> dict[UUID, schemas.Initiative]:
    """The initiative each idea was promoted into, counted, in one query for every idea."""
    if not idea_ids:
        return {}
    query = (
        select(models.Initiative, *issue_count_columns())
        .outerjoin(models.Issue, models.Issue.initiative_id == models.Initiative.id)
        .where(models.Initiative.idea_id.in_(idea_ids))
        .group_by(models.Initiative.id)
    )
    rows = [(row[0], row[1:]) for row in session.execute(query).all()]
    views = initiative_views(session, rows)
    return {initiative.idea_id: view for (initiative, _), view in zip(rows, views, strict=True) if initiative.idea_id is not None}
