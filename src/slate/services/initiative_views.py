"""Build the initiative read shape: the initiative, its issues' counts, its claim, its steps, tags, pursuits and refs."""

from __future__ import annotations

import datetime as dt
from collections.abc import Sequence
from typing import Any
from typing import NamedTuple

from sqlalchemy import Row
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
from slate.services.record_checks import check_requests
from slate.services.record_checks import latest_checks
from slate.services.record_parts import record_parts
from slate.services.record_pursuits import pursuits_of
from slate.services.record_refs import refs_of
from slate.services.record_steps import initiative_step_views
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


class IssueCounts(NamedTuple):
    """An initiative's issues counted by `issue_count_columns()`, read by label. `repos` is None where it has no issue with a repo."""

    issue_count: int
    open_count: int
    completed_count: int
    canceled_count: int
    repos: list[str] | None

    @classmethod
    def of(cls, row: Row[Any]) -> IssueCounts:
        """The counts in a row selecting `issue_count_columns()`, beside whatever else it selects."""
        return cls(**{name: row._mapping[name] for name in cls._fields})


def initiative_view(
    initiative: models.Initiative,
    counts: IssueCounts,
    claim: schemas.Claim | None,
    depends_on: list[schemas.RecordSummary],
    waiting: list[schemas.RecordSummary],
    steps: schemas.InitiativeSteps,
    tags: list[str],
    pursuits: list[schemas.PursuitSummary],
    refs: list[schemas.RecordRef],
    last_check: schemas.LastCheck | None,
    check_request: schemas.CheckRequest | None,
) -> schemas.Initiative:
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
        flow_id=initiative.flow_id,
        autofix=initiative.autofix,
        created_ts=initiative.created_ts,
        closed_ts=initiative.closed_ts,
        tags=tags,
        refs=refs,
        pursuits=pursuits,
        issue_count=counts.issue_count,
        open_count=counts.open_count,
        completed_count=counts.completed_count,
        canceled_count=counts.canceled_count,
        repos=sorted(counts.repos or []),
        claim=claim,
        last_check=last_check,
        check_request=check_request,
        depends_on=depends_on,
        waiting_initiatives=waiting,
        **dict(steps),
    )


def initiative_views(session: Session, rows: Sequence[tuple[models.Initiative, IssueCounts]]) -> list[schemas.Initiative]:
    """Each initiative beside its issues' counts, with its claim and its waits either way."""
    claims = unended_claim_views(session, [initiative.number for initiative, _ in rows], dt.datetime.now(dt.UTC))
    depends_on = waited_on(session, [initiative.id for initiative, _ in rows])
    waiting = waiting_initiatives(session, [initiative.number for initiative, _ in rows])
    steps = initiative_step_views(session, [initiative for initiative, _ in rows])
    tags = own_tags(session, [initiative.number for initiative, _ in rows])
    pursuits = pursuits_of(session, [initiative.number for initiative, _ in rows])
    refs = refs_of(session, [initiative.number for initiative, _ in rows])
    checks = latest_checks(session, [initiative.number for initiative, _ in rows])
    requests = check_requests(session, [initiative.number for initiative, _ in rows])
    return [
        initiative_view(
            initiative,
            counts,
            claims.get(initiative.number),
            depends_on.get(initiative.id, []),
            waiting.get(initiative.number, []),
            steps[initiative.number],
            tags.get(initiative.number, []),
            pursuits.get(initiative.number, []),
            refs.get(initiative.number, []),
            checks.get(initiative.number),
            requests.get(initiative.number),
        )
        for initiative, counts in rows
    ]


def counted(session: Session, initiative: models.Initiative) -> schemas.Initiative:
    counts = session.execute(select(*issue_count_columns()).where(models.Issue.initiative_id == initiative.id)).one()
    return initiative_views(session, [(initiative, IssueCounts.of(counts))])[0]


def counted_initiatives(session: Session, initiatives: Sequence[models.Initiative]) -> list[schemas.Initiative]:
    """Each initiative counted, in the order given, in one query for all of them."""
    query = (
        select(models.Initiative.id, *issue_count_columns())
        .outerjoin(models.Issue, models.Issue.initiative_id == models.Initiative.id)
        .where(models.Initiative.id.in_([initiative.id for initiative in initiatives]))
        .group_by(models.Initiative.id)
    )
    counts = {row.id: IssueCounts.of(row) for row in session.execute(query).all()}
    return initiative_views(session, [(initiative, counts[initiative.id]) for initiative in initiatives])


def initiative_detail(session: Session, initiative: models.Initiative) -> schemas.InitiativeDetail:
    """The initiative counted, with every claim, comment and document it has, as an idea's and an issue's reads carry theirs."""
    return schemas.InitiativeDetail(
        **counted(session, initiative).model_dump(),
        **record_parts(session, initiative.number, dt.datetime.now(dt.UTC)).model_dump(),
    )
