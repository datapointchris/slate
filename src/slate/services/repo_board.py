"""Every repo's issues on one board: counted by state, closed per day and per week, and the ready ones by how long they have waited.

A repo is the name on an issue rather than a record, so the board groups every issue by that name,
and the issues on no repo make a row of their own. One readiness pass, one read of the live claims
and one read of the issues serve every row.

A ready issue has waited since it was filed. Readiness is derived on every read and never stored,
so no moment it became ready is recorded to count from.
"""

from __future__ import annotations

import datetime as dt
from collections import Counter
from collections import defaultdict
from collections.abc import Iterable
from collections.abc import Sequence
from typing import Any
from zoneinfo import ZoneInfo

from sqlalchemy import select
from sqlalchemy.orm import Session
from sqlalchemy.orm import load_only

from slate import models
from slate import schemas
from slate.services.claim_views import live_claims_on_every_record
from slate.services.initiative_board import CloseCalendar
from slate.services.initiative_board import board_issue
from slate.services.initiative_board import close_moments
from slate.services.initiative_board import counts_of
from slate.services.issue_readiness import IssueReadiness
from slate.services.issue_readiness import measure_readiness

CLOSES_BY_WEEK = 12

type Issues = list[tuple[models.Issue, schemas.BoardIssue]]


def week_starts(today: dt.date, count: int) -> list[dt.date]:
    """The Monday of each of the last `count` weeks, oldest first, the last that of `today`."""
    monday = today - dt.timedelta(days=today.weekday())
    return [monday - dt.timedelta(weeks=back) for back in reversed(range(count))]


def closes_by_week(moments: Iterable[dt.datetime], weeks: Sequence[dt.date], zone: ZoneInfo) -> list[int]:
    """How many of `moments` fall in each week starting on a day of `weeks`, by the calendar in `zone`."""

    def monday(moment: dt.datetime) -> dt.date:
        day = moment.astimezone(zone).date()
        return day - dt.timedelta(days=day.weekday())

    on_week = Counter(monday(moment) for moment in moments)
    return [on_week[week] for week in weeks]


def repo_board(session: Session, zone: str) -> schemas.RepoBoard:
    """The board of every repo as of now, its calendar read in `zone`."""
    calendar = CloseCalendar.as_of(zone)
    weeks = week_starts(calendar.today, CLOSES_BY_WEEK)
    readiness = measure_readiness(session, calendar.today, calendar.now)
    claims = live_claims_on_every_record(session, calendar.now)
    columns = (
        models.Issue.number,
        models.Issue.title,
        models.Issue.status,
        models.Issue.type,
        models.Issue.priority,
        models.Issue.repo,
        models.Issue.created_ts,
        models.Issue.closed_ts,
    )
    in_repo: dict[str | None, Issues] = defaultdict(list)
    for issue in session.scalars(select(models.Issue).options(load_only(*columns))):
        in_repo[issue.repo].append((issue, board_issue(issue, claims.get(issue.number), readiness)))

    def rollup(issues: Issues) -> dict[str, Any]:
        rows = [row for _, row in issues]
        return {**counts_of(rows, calendar), 'closes_by_week': closes_by_week(close_moments(rows), weeks, calendar.zone)}

    every = [pair for issues in in_repo.values() for pair in issues]
    return schemas.RepoBoard(
        read_ts=calendar.now,
        zone=zone,
        days=calendar.days,
        weeks=weeks,
        totals=schemas.RepoRollup(**rollup(every)),
        repos=[
            schemas.BoardRepo(repo=repo, ready=ready_issues(in_repo[repo], readiness), **rollup(in_repo[repo]))
            for repo in sorted(in_repo, key=lambda repo: (repo is None, repo or ''))
        ],
    )


def ready_issues(issues: Issues, readiness: IssueReadiness) -> list[schemas.ReadyIssue]:
    waiting = sorted((issue for issue, row in issues if row.state == 'ready'), key=lambda issue: (issue.created_ts, issue.number))
    return [
        schemas.ReadyIssue(
            number=issue.number,
            title=issue.title,
            effective_priority=readiness.effective_priority.get(issue.id, issue.priority),
            created_ts=issue.created_ts,
        )
        for issue in waiting
    ]
