"""Build the initiative read shape: the initiative, the counts of its issues, and its claim."""

from __future__ import annotations

import datetime as dt
from collections.abc import Sequence
from typing import Any

from sqlalchemy import distinct
from sqlalchemy import func
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.issue import CLOSED_ISSUE_STATUSES
from slate.services.claim_views import unended_claim_views


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


def initiative_view(initiative: models.Initiative, counts: Sequence[Any], claim: schemas.Claim | None) -> schemas.Initiative:
    """`counts` is a row of `issue_count_columns()`."""
    issue_count, open_count, completed_count, canceled_count, repos = counts
    return schemas.Initiative(
        id=initiative.id,
        number=initiative.number,
        name=initiative.name,
        description=initiative.description,
        status=initiative.status,
        status_reason=initiative.status_reason,
        priority=initiative.priority,
        position=initiative.position,
        autofix=initiative.autofix,
        created_ts=initiative.created_ts,
        closed_ts=initiative.closed_ts,
        issue_count=issue_count,
        open_count=open_count,
        completed_count=completed_count,
        canceled_count=canceled_count,
        repos=sorted(repos) if repos else [],
        claim=claim,
    )


def initiative_views(session: Session, rows: Sequence[tuple[models.Initiative, Sequence[Any]]]) -> list[schemas.Initiative]:
    """Each initiative beside its row of `issue_count_columns()`, with its claim."""
    claims = unended_claim_views(session, [initiative.number for initiative, _ in rows], dt.datetime.now(dt.UTC))
    return [initiative_view(initiative, counts, claims.get(initiative.number)) for initiative, counts in rows]


def counted(session: Session, initiative: models.Initiative) -> schemas.Initiative:
    counts = session.execute(select(*issue_count_columns()).where(models.Issue.initiative_id == initiative.id)).one()
    return initiative_views(session, [(initiative, counts)])[0]
