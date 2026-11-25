"""Every kind of open work in the order to take it, and the held work apart from it. `GET /queue` answers it.

Each row carries the step taking it means:

- `discuss` for an idea nobody has promoted
- `shape`, `design` or `preflight` for an active initiative, the first of them it plans past its furthest
  checked step, unless it checked none and holds open issues
- `decide` for a ready decision issue
- `build` for any other ready issue, and for a held initiative whose issues are being built or that has
  no step left to offer
- `retro` for an active initiative that plans one, past its steps before building with no unclosed issue

An initiative's step reads its stage from `services/record_steps.py` and skips each step it marks as
not planned, so a step it will not run is never offered. An initiative at `build` is not a row of
its own, because its issues are, and neither is one with no step left to offer unless it is held.
One offered `design` or `preflight` is a row beside its open issues, so a step never holds work back. A someday
initiative is set aside, so neither it nor its shelved issues are rows. The order is the slots
`services/issue_readiness.py` derives, so an initiative's work runs together.

A live claim says someone is working the record, so a held record is listed under its holder rather
than at its place in the queue. Held rows are whatever the record is, ready or not.

A live claim on an initiative says someone is running its plan, so each ready issue in that plan to
build is listed under that holder too, naming the initiative as `held_through`. A decision stays
queued, because it waits on a person whoever coordinates. Readiness ignores the initiative's claim,
so a worker's own claim and every other reader of readiness see the issue as ready.

`queued_issues` and `initiative_row_step` say which records are rows, and `QueuePlacement` places
them, for `GET /queue` here and for `GET /queue/board` in `services/queue_board.py`. So both reads
list the same rows in the same order.
"""

from __future__ import annotations

import datetime as dt
from collections.abc import Collection
from collections.abc import Iterable
from collections.abc import Mapping
from collections.abc import Sequence
from dataclasses import dataclass
from dataclasses import field
from typing import Any

from sqlalchemy import or_
from sqlalchemy import select
from sqlalchemy.orm import Session
from sqlalchemy.orm.interfaces import ORMOption

from slate import models
from slate import schemas
from slate.models.record import Kind
from slate.models.worklog import BUILD
from slate.models.worklog import STEPS_AFTER_BUILD
from slate.models.worklog import STEPS_BEFORE_BUILD
from slate.models.worklog import WORKLOG_STEPS
from slate.schemas.queue import IdeaRow
from slate.schemas.queue import InitiativeRow
from slate.schemas.queue import IssueRow
from slate.schemas.queue import QueueRowBase
from slate.schemas.queue import Step
from slate.schemas.ready_filter import ReadyFilter
from slate.schemas.ready_filter import RecordGroupings
from slate.services.claim_views import live_claims_on_every_record
from slate.services.held_plans import held_plans
from slate.services.held_plans import holding_claim
from slate.services.idea_views import idea_views
from slate.services.initiative_views import initiative_views
from slate.services.initiative_views import issue_count_columns
from slate.services.issue_filters import apply_label_filter
from slate.services.issue_filters import apply_repo_filter
from slate.services.issue_filters import autofixable_filtered
from slate.services.issue_paths import HeldOverlap
from slate.services.issue_paths import held_overlaps
from slate.services.issue_readiness import PINNED
from slate.services.issue_readiness import IssueReadiness
from slate.services.issue_readiness import QueueKey
from slate.services.issue_readiness import measure_readiness
from slate.services.issue_readiness import slot_placed_by
from slate.services.issue_views import ISSUE_LOAD_OPTIONS
from slate.services.issue_views import calendar_now
from slate.services.issue_views import issue_views
from slate.services.open_records import is_open
from slate.services.record_groupings import narrowed_to_groupings
from slate.services.record_lookup import resolve_initiative
from slate.services.record_work import record_work
from slate.services.vocabulary import validate_lookup

type Row = IssueRow | InitiativeRow | IdeaRow


def issue_step(issue_type: str) -> Step:
    return 'decide' if issue_type == 'decision' else 'build'


def initiative_step(stage: str, not_planned_steps: Collection[str], open_count: int) -> Step | None:
    """The first planned step before building past `stage`, else `build` while its issues are the work, else the first planned after.

    None is no step to offer, which is one planning no step after building once its issues close.
    """
    passed = 0 if stage == 'idea' else WORKLOG_STEPS.index(stage) + 1
    for step in STEPS_BEFORE_BUILD[passed:]:
        if step not in not_planned_steps:
            return step
    if open_count:
        return BUILD
    return next((step for step in STEPS_AFTER_BUILD if step not in not_planned_steps), None)


def initiative_row_step(
    number: int, steps: schemas.InitiativeSteps, open_count: int, readiness: IssueReadiness, held: Collection[int]
) -> Step | None:
    """The step an open initiative lists at, or None where it is no row of its own.

    It lists alone at a step other than `build` where the queue holds its slot, and under its holder
    whenever held. One with no step left is still its holder's while held, so it lists at the build
    its issues carry.
    """
    offered = initiative_step(steps.stage, steps.not_planned_steps, open_count)
    step = BUILD if offered is None else offered
    queued_alone = step != BUILD and number in readiness.slot_key_by_number
    return step if queued_alone or number in held else None


@dataclass
class QueuePlacement[PlacedRow: QueueRowBase]:
    """The one placement step of every queue read: a row goes under the claim holding it, else into the queue at its key."""

    held: Mapping[int, models.Claim]
    plans: Mapping[int, models.Claim]
    queued: list[tuple[QueueKey, PlacedRow]] = field(default_factory=list)
    claimed: list[tuple[str, QueueKey, PlacedRow]] = field(default_factory=list)

    def place(self, key: QueueKey, row: PlacedRow) -> models.Claim | None:
        """Place `row` at `key`, and answer the claim holding it, or None where it is queued."""
        row.pinned = key.pin == PINNED
        claim = holding_claim(row.number, row.step, self.held, self.plans)
        if claim is None:
            self.queued.append((key, row))
            return None
        if claim.record_number != row.number:
            row.held_through = claim.record_number
        row.holder = claim.holder
        self.claimed.append((claim.holder, key, row))
        return claim

    def queue_rows(self) -> list[PlacedRow]:
        """The unheld rows, first row next."""
        return [row for _, row in sorted(self.queued, key=lambda pair: pair[0])]

    def claimed_rows(self) -> list[PlacedRow]:
        """The held rows, by holder, then in queue order."""
        return [row for _, _, row in sorted(self.claimed, key=lambda entry: (entry[0], entry[1]))]


def path_overlaps(overlaps: Iterable[HeldOverlap]) -> list[schemas.PathOverlap]:
    return [
        schemas.PathOverlap(
            number=overlap.number,
            title=overlap.title,
            holder=overlap.claim.holder,
            expires_ts=overlap.claim.expires_ts,
            paths=overlap.paths,
        )
        for overlap in overlaps
    ]


# A queue filter narrows ideas and initiatives too when it is a grouping, or the initiative whose own row
# and issues it keeps. Every other filter reads only issues, so one added to `ReadyFilter` leaves them out.
ISSUE_ONLY_FILTERS = frozenset(ReadyFilter.model_fields) - frozenset(RecordGroupings.model_fields) - {'initiative'}


def reads_only_issues(filters: schemas.QueueFilter) -> bool:
    return any(getattr(filters, name) is not None for name in ISSUE_ONLY_FILTERS)


def wanted(filters: schemas.QueueFilter, step: Step) -> bool:
    return filters.step is None or filters.step == step


def queued_issues(
    session: Session,
    readiness: IssueReadiness,
    filters: ReadyFilter,
    step: Step | None,
    held: Collection[int] = (),
    within: Collection[int] | None = None,
    options: Sequence[ORMOption] = ISSUE_LOAD_OPTIONS,
) -> list[models.Issue]:
    """The ready issues at `step`, and the unclosed ones `held` names, narrowed by `filters`, in queue order.

    `within` replaces the first two with these numbers, ready or not. `GET /queue` lists them, and
    `POST /claims/next` holds the first of them at step `build`. `options` say what each issue loads.
    """
    query = select(models.Issue).options(*options)
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
    query = narrowed_to_groupings(query, Kind.ISSUE, filters, session)
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
    placement: QueuePlacement[Row] = QueuePlacement(held, held_plans(session, held) if plans_hold and filters.for_record is None else {})

    work = record_work(session, filters.for_record) if filters.for_record is not None else None
    reads_other_kinds = not reads_only_issues(filters)
    initiative = resolve_initiative(session, filters.initiative) if filters.initiative is not None else None
    if work is not None:
        reads_other_kinds = reads_other_kinds and work.kind != Kind.ISSUE
        if work.kind == Kind.INITIATIVE and initiative is None:
            initiative = resolve_initiative(session, work.number)

    if filters.step in (None, 'decide', 'build'):
        issues = queued_issues(session, readiness, filters, filters.step, held, None if work is None else work.unblocks)
        overlaps = held_overlaps(session, issues, now)
        for issue, view in zip(issues, issue_views(session, issues, zone, readiness), strict=True):
            issue_row = IssueRow(
                number=issue.number,
                title=issue.title,
                step=issue_step(issue.type),
                priority=view.effective_priority,
                placed_by=readiness.placed_by[issue.id],
                raised_by=readiness.raised_by.get(issue.id),
                overlaps=path_overlaps(overlaps.get(issue.number, [])),
                unblocks=[] if work is None else work.unblocks.get(issue.number, []),
                record=view,
            )
            placement.place(readiness.sort_key(issue), issue_row)

    if reads_other_kinds and filters.step not in ('discuss', 'decide') and (work is None or work.kind == Kind.INITIATIVE):
        for initiative_view in open_initiatives(session, initiative, filters):
            step = initiative_row_step(initiative_view.number, initiative_view, initiative_view.open_count, readiness, held)
            if step is None or not wanted(filters, step):
                continue
            key = readiness.record_sort_key(initiative_view.number, initiative_view.priority, initiative_view.rank)
            initiative_row = InitiativeRow(
                number=initiative_view.number,
                title=initiative_view.name,
                step=step,
                priority=initiative_view.priority,
                placed_by=slot_placed_by(key),
                record=initiative_view,
            )
            placement.place(key, initiative_row)

    if reads_other_kinds and initiative is None and wanted(filters, 'discuss'):
        query = select(models.Idea).where(is_open(Kind.IDEA))
        query = narrowed_to_groupings(query, Kind.IDEA, filters, session)
        if work is not None:
            query = query.where(models.Idea.number == work.number)
        ideas = session.scalars(query).all()
        for idea, idea_view in zip(ideas, idea_views(session, ideas), strict=True):
            key = readiness.record_sort_key(idea.number, idea.priority, idea.rank)
            idea_row = IdeaRow(
                number=idea_view.number,
                title=idea_view.title,
                step='discuss',
                priority=idea_view.priority,
                placed_by=slot_placed_by(key),
                record=idea_view,
            )
            placement.place(key, idea_row)

    queue = placement.queue_rows()
    return schemas.Queue(queue=queue if limit is None else queue[:limit], claimed=placement.claimed_rows())


def open_initiative_rows(
    session: Session, only: models.Initiative | None, groupings: RecordGroupings, *options: ORMOption
) -> list[tuple[models.Initiative, Sequence[Any]]]:
    """Active initiatives, and someday ones too since a claim may hold one, each with a row of `issue_count_columns()`.

    `only` narrows to one, and `groupings` to those in each grouping it names. `options` say what each initiative loads.
    """
    query: Any = (
        select(models.Initiative, *issue_count_columns())
        .options(*options)
        .outerjoin(models.Issue, models.Issue.initiative_id == models.Initiative.id)
        .where(is_open(Kind.INITIATIVE))
        .group_by(models.Initiative.id)
    )
    if only is not None:
        query = query.where(models.Initiative.id == only.id)
    query = narrowed_to_groupings(query, Kind.INITIATIVE, groupings, session)
    return [(row[0], row[1:]) for row in session.execute(query).all()]


def open_initiatives(session: Session, only: models.Initiative | None, groupings: RecordGroupings) -> list[schemas.Initiative]:
    return initiative_views(session, open_initiative_rows(session, only, groupings))
