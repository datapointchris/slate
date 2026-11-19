"""The order open issues and initiatives come due for a check, which `GET /checks/due` answers.

slate decides what is due, so two readers, or one that restarted, read the same answer and keep no
state of their own. Each record comes due at the first rung it meets:

1. `incoming`: an issue nobody has placed yet, most urgent first, then oldest
2. `requested`: a record someone marked to be checked next, the oldest mark first
3. `never_checked`: a record with no check, in queue order
4. `queue_head`: one of the queue's first `head_rows` rows whose last check is `head_days` old
5. `stale`: any other record whose last check is `days` old, the oldest check first

A record a live claim holds is never due, because a worker holds it. That counts each issue in the
plan of a claimed initiative, as the queue lists it under that holder. Ideas hold no work yet to
check, and a closed record holds none any more, so neither is ever due.
"""

from __future__ import annotations

import datetime as dt
from dataclasses import dataclass
from typing import Any
from typing import Literal

from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.config import CheckSettings
from slate.models.record import Kind
from slate.schemas.record_check import DueBecause
from slate.services.claim_views import live_claims_on_every_record
from slate.services.held_plans import held_plans
from slate.services.issue_readiness import IssueReadiness
from slate.services.issue_readiness import QueueKey
from slate.services.issue_readiness import measure_readiness
from slate.services.issue_readiness import slot_key
from slate.services.issue_readiness import urgency
from slate.services.issue_views import calendar_now
from slate.services.likely_duplicates import likely_duplicates_of_open
from slate.services.open_records import is_open
from slate.services.record_checks import latest_checks
from slate.services.work_queue import queue_as_measured

INCOMING = 'incoming'


@dataclass(frozen=True)
class Candidate:
    """An open issue or initiative. `priority` is an issue's effective priority, else the initiative's own."""

    number: int
    kind: Literal['issue', 'initiative']
    title: str
    status: str
    priority: int
    repo: str | None
    created_ts: dt.datetime
    queue_key: QueueKey
    check_request: schemas.CheckRequest | None


def requested(row: Any) -> schemas.CheckRequest | None:
    if row.check_requested_ts is None:
        return None
    return schemas.CheckRequest(requested_ts=row.check_requested_ts, requested_by=row.check_requested_by, reason=row.check_requested_reason)


def open_candidates(session: Session, readiness: IssueReadiness) -> list[Candidate]:
    marks = (models.Record.rank, models.Record.check_requested_ts, models.Record.check_requested_by, models.Record.check_requested_reason)
    issues = session.execute(
        select(
            models.Issue.id,
            models.Issue.number,
            models.Issue.title,
            models.Issue.status,
            models.Issue.priority,
            models.Issue.repo,
            models.Issue.created_ts,
            *marks,
        )
        .join(models.Record, models.Record.number == models.Issue.number)
        .where(is_open(Kind.ISSUE))
    ).all()
    initiatives = session.execute(
        select(
            models.Initiative.number,
            models.Initiative.name,
            models.Initiative.status,
            models.Initiative.priority,
            models.Initiative.created_ts,
            *marks,
        )
        .join(models.Record, models.Record.number == models.Initiative.number)
        .where(is_open(Kind.INITIATIVE))
    ).all()
    return [
        *(
            Candidate(
                number=row.number,
                kind='issue',
                title=row.title,
                status=row.status,
                priority=readiness.effective_priority.get(row.id, row.priority),
                repo=row.repo,
                created_ts=row.created_ts,
                queue_key=readiness.queue_key.get(row.id) or slot_key(urgency(row.priority), row.rank, row.number),
                check_request=requested(row),
            )
            for row in issues
        ),
        *(
            Candidate(
                number=row.number,
                kind='initiative',
                title=row.name,
                status=row.status,
                priority=row.priority,
                repo=None,
                created_ts=row.created_ts,
                queue_key=readiness.record_sort_key(row.number, row.priority, row.rank),
                check_request=requested(row),
            )
            for row in initiatives
        ),
    ]


def due_checks(session: Session, zone: str, settings: CheckSettings, limit: int | None) -> list[schemas.DueCheck]:
    today, now = calendar_now(zone)
    readiness = measure_readiness(session, today, now)
    held = live_claims_on_every_record(session, now)
    holding = held.keys() | held_plans(session, held).keys()
    candidates = [candidate for candidate in open_candidates(session, readiness) if candidate.number not in holding]
    checks = latest_checks(session, [candidate.number for candidate in candidates])
    head = queue_as_measured(session, zone, now, readiness, schemas.QueueFilter.model_validate({}), settings.head_rows).queue
    head_place = {row.number: place for place, row in enumerate(head)}
    head_cutoff, cutoff = now - dt.timedelta(days=settings.head_days), now - dt.timedelta(days=settings.days)

    rungs: dict[DueBecause, list[tuple[Any, Candidate]]] = {
        'incoming': [],
        'requested': [],
        'never_checked': [],
        'queue_head': [],
        'stale': [],
    }
    for candidate in candidates:
        last = checks.get(candidate.number)
        if candidate.kind == 'issue' and candidate.status == INCOMING:
            rungs['incoming'].append(((urgency(candidate.priority), candidate.created_ts, candidate.number), candidate))
        elif candidate.check_request is not None:
            rungs['requested'].append(((candidate.check_request.requested_ts, candidate.number), candidate))
        elif last is None:
            rungs['never_checked'].append((candidate.queue_key, candidate))
        elif candidate.number in head_place and last.checked_ts < head_cutoff:
            rungs['queue_head'].append((head_place[candidate.number], candidate))
        elif last.checked_ts < cutoff:
            rungs['stale'].append(((last.checked_ts, candidate.number), candidate))

    due = [(rung, candidate) for rung, placed in rungs.items() for _, candidate in sorted(placed, key=lambda pair: pair[0])]
    if limit is not None:
        due = due[:limit]
    duplicates = likely_duplicates_of_open(session, [candidate.number for _, candidate in due])
    return [
        schemas.DueCheck(
            number=candidate.number,
            kind=candidate.kind,
            title=candidate.title,
            status=candidate.status,
            priority=candidate.priority,
            repo=candidate.repo,
            due_because=rung,
            last_check=checks.get(candidate.number),
            check_request=candidate.check_request,
            likely_duplicates=[
                schemas.RecordSummary(number=match.number, kind=match.kind, title=match.title)
                for match in duplicates.get(candidate.number, [])
            ],
        )
        for rung, candidate in due
    ]
