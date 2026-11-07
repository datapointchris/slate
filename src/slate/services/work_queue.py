"""Every kind of open work in the order to take it, and the held work apart from it. `GET /queue` answers it.

Each row carries the step taking it means:

- `discuss` for an idea nobody has promoted
- `plan` for an active initiative with no unclosed issue
- `decide` for a ready decision issue
- `build` for any other ready issue, and for a held initiative whose issues are being built

An initiative holding unclosed issues is not a row of its own, because its issues are. A someday
initiative is set aside, so neither it nor its shelved issues are rows. The order is the slots
`services/issue_readiness.py` derives, so an initiative's work runs together.

A live claim says someone is working the record, so a held record is listed under its holder rather
than at its place in the queue. Held rows are whatever the record is, ready or not.

A live claim on an initiative says someone is running its plan, so each ready issue in that plan to
build is listed under that holder too, naming the initiative as `held_through`. A decision stays
queued, because it waits on a person whoever coordinates. Readiness ignores the initiative's claim,
so a worker's own claim and every other reader of readiness see the issue as ready.
"""

from __future__ import annotations

import datetime as dt
from collections.abc import Collection
from dataclasses import dataclass
from typing import Any

from sqlalchemy import or_
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.record import Kind
from slate.schemas.queue import IdeaRow
from slate.schemas.queue import InitiativeRow
from slate.schemas.queue import IssueRow
from slate.schemas.queue import Step
from slate.schemas.ready_filter import ReadyFilter
from slate.services.claim_views import live_claims_on_every_record
from slate.services.held_plans import held_plans
from slate.services.idea_views import IDEA_LOAD_OPTIONS
from slate.services.idea_views import idea_views
from slate.services.initiative_views import initiative_views
from slate.services.initiative_views import issue_count_columns
from slate.services.issue_filters import apply_label_filter
from slate.services.issue_filters import apply_repo_filter
from slate.services.issue_filters import autofixable_filtered
from slate.services.issue_paths import held_overlaps
from slate.services.issue_readiness import PINNED
from slate.services.issue_readiness import IssueReadiness
from slate.services.issue_readiness import QueueKey
from slate.services.issue_readiness import measure_readiness
from slate.services.issue_views import ISSUE_LOAD_OPTIONS
from slate.services.issue_views import calendar_now
from slate.services.issue_views import issue_views
from slate.services.open_records import is_open
from slate.services.record_refs import resolve_initiative
from slate.services.record_work import record_work
from slate.services.vocabulary import validate_lookup

type Row = IssueRow | InitiativeRow | IdeaRow


def issue_step(issue_type: str) -> Step:
    return 'decide' if issue_type == 'decision' else 'build'


def reads_only_issues(filters: schemas.QueueFilter) -> bool:
    return any(value is not None for value in (filters.repo, filters.type, filters.label, filters.autofixable))


def wanted(filters: schemas.QueueFilter, step: Step) -> bool:
    return filters.step is None or filters.step == step


def queued_issues(
    session: Session,
    readiness: IssueReadiness,
    filters: ReadyFilter,
    step: Step | None,
    held: Collection[int] = (),
    within: Collection[int] | None = None,
) -> list[models.Issue]:
    """The ready issues at `step`, and the unclosed ones `held` names, narrowed by `filters`, in queue order.

    `within` replaces the first two with these numbers, ready or not. `GET /queue` lists them, and
    `POST /claims/next` holds the first of them at step `build`.
    """
    query = select(models.Issue).options(*ISSUE_LOAD_OPTIONS)
    if within is not None:
        query = query.where(models.Issue.number.in_(list(within)) & is_open(Kind.ISSUE))
    else:
        query = query.where(or_(models.Issue.id.in_(readiness.ready), models.Issue.number.in_(list(held)) & is_open(Kind.ISSUE)))
    if filters.type is not None:
        validate_lookup(session, models.IssueType, filters.type, 'issue type')
        query = query.where(models.Issue.type == filters.type)
    if step == 'decide':
        query = query.where(models.Issue.type == 'decision')
    elif step == 'build':
        query = query.where(models.Issue.type != 'decision')
    if filters.initiative is not None:
        query = query.where(models.Issue.initiative_id == resolve_initiative(session, filters.initiative).id)
    query = apply_label_filter(apply_repo_filter(query, filters.repo), filters.label, session)
    issues = sorted(session.scalars(query).all(), key=readiness.sort_key)
    return autofixable_filtered(issues, readiness, filters.autofixable)


@dataclass(frozen=True)
class HoldCandidates:
    """`passed_over` holds each ready issue another holder's plan keeps from `issues`, with that holder's claim."""

    issues: list[models.Issue]
    passed_over: dict[int, models.Claim]


def candidates_to_hold(session: Session, readiness: IssueReadiness, filters: ReadyFilter, holder: str, now: dt.datetime) -> HoldCandidates:
    """The issues `POST /claims/next` may hold, in queue order: step `build`, less the plan of an initiative another holder claims."""
    plans = held_plans(session, live_claims_on_every_record(session, now))
    ready = queued_issues(session, readiness, filters, 'build')
    passed_over = {issue.number: plans[issue.number] for issue in ready if issue.number in plans and plans[issue.number].holder != holder}
    return HoldCandidates(issues=[issue for issue in ready if issue.number not in passed_over], passed_over=passed_over)


def read_queue(session: Session, zone: str, filters: schemas.QueueFilter, limit: int | None) -> schemas.Queue:
    today, now = calendar_now(zone)
    return queue_as_measured(session, zone, now, measure_readiness(session, today, now), filters, limit)


def queue_as_measured(
    session: Session,
    zone: str,
    now: dt.datetime,
    readiness: IssueReadiness,
    filters: schemas.QueueFilter,
    limit: int | None,
    plans_hold: bool = True,
) -> schemas.Queue:
    """The queue from readiness already measured at `now`, so a read that needs readiness for more shares its pass and its order.

    `plans_hold` lists a held initiative's plan under its holder. `for_record` reads one plan whole, so nothing moves under it.
    """
    held = live_claims_on_every_record(session, now)
    plans = held_plans(session, held) if plans_hold and filters.for_record is None else {}
    queued: list[tuple[QueueKey, Row]] = []
    claimed: list[tuple[str, QueueKey, Row]] = []

    def place(key: QueueKey, row: Row) -> None:
        row.pinned = key.pin == PINNED
        claim = held.get(row.number)
        if claim is None and row.step == 'build' and (claim := plans.get(row.number)) is not None:
            row.held_through = claim.record_number
        if claim is None:
            queued.append((key, row))
        else:
            row.holder = claim.holder
            claimed.append((claim.holder, key, row))

    work = record_work(session, filters.for_record) if filters.for_record is not None else None
    reads_other_kinds = not reads_only_issues(filters)
    initiative = resolve_initiative(session, filters.initiative) if filters.initiative is not None else None
    if work is not None:
        reads_other_kinds = reads_other_kinds and work.kind != Kind.ISSUE
        if work.kind == Kind.INITIATIVE and initiative is None:
            initiative = resolve_initiative(session, work.number)

    if filters.step not in ('discuss', 'plan'):
        issues = queued_issues(session, readiness, filters, filters.step, held, None if work is None else work.unblocks)
        overlaps = held_overlaps(session, issues, now)
        for issue, view in zip(issues, issue_views(session, issues, zone, readiness), strict=True):
            issue_row = IssueRow(
                number=issue.number,
                title=issue.title,
                step=issue_step(issue.type),
                priority=view.effective_priority,
                raised_by=readiness.raised_by.get(issue.id),
                overlaps=[
                    schemas.PathOverlap(
                        number=overlap.number,
                        title=overlap.title,
                        holder=overlap.claim.holder,
                        expires_ts=overlap.claim.expires_ts,
                        paths=overlap.paths,
                    )
                    for overlap in overlaps.get(issue.number, [])
                ],
                unblocks=[] if work is None else work.unblocks.get(issue.number, []),
                record=view,
            )
            place(readiness.sort_key(issue), issue_row)

    if reads_other_kinds and filters.step in (None, 'plan', 'build') and (work is None or work.kind == Kind.INITIATIVE):
        for initiative_view in open_initiatives(session, initiative):
            step: Step = 'build' if initiative_view.open_count else 'plan'
            queued_alone = step == 'plan' and initiative_view.number in readiness.slot_key_by_number
            if not wanted(filters, step) or not (queued_alone or initiative_view.number in held):
                continue
            initiative_row = InitiativeRow(
                number=initiative_view.number,
                title=initiative_view.name,
                step=step,
                priority=initiative_view.priority,
                record=initiative_view,
            )
            place(readiness.record_sort_key(initiative_view.number, initiative_view.priority, initiative_view.rank), initiative_row)

    if reads_other_kinds and initiative is None and wanted(filters, 'discuss'):
        query = select(models.Idea).options(*IDEA_LOAD_OPTIONS).where(is_open(Kind.IDEA))
        if work is not None:
            query = query.where(models.Idea.number == work.number)
        ideas = session.scalars(query).all()
        for idea_view in idea_views(session, ideas):
            idea_row = IdeaRow(
                number=idea_view.number, title=idea_view.title, step='discuss', priority=idea_view.priority, record=idea_view
            )
            place(readiness.record_sort_key(idea_view.number, idea_view.priority, idea_view.rank), idea_row)

    queue = [row for _, row in sorted(queued, key=lambda pair: pair[0])]
    return schemas.Queue(
        queue=queue if limit is None else queue[:limit],
        claimed=[row for _, _, row in sorted(claimed, key=lambda entry: (entry[0], entry[1]))],
    )


def open_initiatives(session: Session, only: models.Initiative | None) -> list[schemas.Initiative]:
    """Active initiatives, and someday ones too since a claim may hold one, each counted. `only` narrows to one."""
    query: Any = (
        select(models.Initiative, *issue_count_columns())
        .outerjoin(models.Issue, models.Issue.initiative_id == models.Initiative.id)
        .where(is_open(Kind.INITIATIVE))
        .group_by(models.Initiative.id)
    )
    if only is not None:
        query = query.where(models.Initiative.id == only.id)
    rows = [(row[0], row[1:]) for row in session.execute(query).all()]
    return initiative_views(session, rows)
