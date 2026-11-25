"""Every open row of the queue, slim, for a board that polls it.

`queued_issues`, `initiative_row_step` and `QueuePlacement` in `services/work_queue.py` choose and
place its rows, so it lists `GET /queue`'s rows in `GET /queue`'s order. It reads only what each row
draws, and builds no `issue_views`, `initiative_views` or `idea_views` row, which join each
record's refs, claim, checks and steps. Its queries stay the same however many rows it answers.
`test_the_board_lists_the_queues_rows_in_its_order_slim_in_the_same_queries_however_many` pins it.
"""

from __future__ import annotations

from sqlalchemy import select
from sqlalchemy.orm import Session
from sqlalchemy.orm import load_only
from sqlalchemy.orm import selectinload

from slate import models
from slate import schemas
from slate.models.record import Kind
from slate.services.claim_views import live_claims_on_every_record
from slate.services.held_plans import held_plans
from slate.services.issue_paths import held_overlaps
from slate.services.issue_readiness import QueueKey
from slate.services.issue_readiness import measure_readiness
from slate.services.issue_readiness import slot_placed_by
from slate.services.issue_views import calendar_now
from slate.services.open_records import is_open
from slate.services.record_pursuits import issue_pursuits
from slate.services.record_pursuits import pursuits_of
from slate.services.record_steps import initiative_step_views
from slate.services.work_queue import QueuePlacement
from slate.services.work_queue import initiative_row_step
from slate.services.work_queue import issue_step
from slate.services.work_queue import open_initiative_rows
from slate.services.work_queue import path_overlaps
from slate.services.work_queue import queued_issues

ISSUE_COLUMNS = (
    load_only(
        models.Issue.number,
        models.Issue.title,
        models.Issue.type,
        models.Issue.priority,
        models.Issue.rank,
        models.Issue.initiative_id,
        models.Issue.repo,
        models.Issue.paths,
    ),
    selectinload(models.Issue.initiative).load_only(models.Initiative.number),
)
INITIATIVE_COLUMNS = load_only(models.Initiative.number, models.Initiative.name, models.Initiative.priority, models.Initiative.rank)


def queue_board(session: Session, zone: str) -> schemas.QueueBoard:
    """The queue as of now, every row slim, its deferral days read in `zone`."""
    today, now = calendar_now(zone)
    readiness = measure_readiness(session, today, now)
    held = live_claims_on_every_record(session, now)
    placement: QueuePlacement[schemas.QueueBoardRow] = QueuePlacement(held, held_plans(session, held))

    def place(key: QueueKey, row: schemas.QueueBoardRow) -> None:
        claim = placement.place(key, row)
        if claim is not None:
            wait = claim.open_wait
            row.expires_ts = claim.expires_ts
            row.waiting = None if wait is None else schemas.ClaimWait.model_validate(wait)

    every_row = schemas.QueueFilter.model_validate({})
    issues = queued_issues(session, readiness, every_row, None, held, options=ISSUE_COLUMNS)
    overlaps = held_overlaps(session, issues, now)
    pursuits_of_issue = issue_pursuits(session, issues)
    for issue in issues:
        place(
            readiness.sort_key(issue),
            schemas.QueueBoardRow(
                kind='issue',
                number=issue.number,
                title=issue.title,
                step=issue_step(issue.type),
                priority=readiness.effective_priority[issue.id],
                own_priority=issue.priority,
                placed_by=readiness.placed_by[issue.id],
                raised_by=readiness.raised_by.get(issue.id),
                initiative=issue.initiative.number if issue.initiative is not None else None,
                repos=[] if issue.repo is None else [issue.repo],
                pursuits=pursuits_of_issue[issue.number],
                overlaps=path_overlaps(overlaps.get(issue.number, [])),
            ),
        )

    initiatives = open_initiative_rows(session, None, every_row, INITIATIVE_COLUMNS)
    ideas = session.execute(
        select(models.Idea.number, models.Idea.title, models.Idea.priority, models.Record.rank)
        .join(models.Record, models.Record.number == models.Idea.number)
        .where(is_open(Kind.IDEA))
    ).all()
    steps = initiative_step_views(session, [initiative for initiative, _ in initiatives])
    pursuits = pursuits_of(session, [*(initiative.number for initiative, _ in initiatives), *(idea.number for idea in ideas)])

    for initiative, counts in initiatives:
        _, open_count, _, _, repos = counts
        step = initiative_row_step(initiative.number, steps[initiative.number], open_count, readiness, held)
        if step is None:
            continue
        key = readiness.record_sort_key(initiative.number, initiative.priority, initiative.rank)
        place(
            key,
            schemas.QueueBoardRow(
                kind='initiative',
                number=initiative.number,
                title=initiative.name,
                step=step,
                priority=initiative.priority,
                own_priority=initiative.priority,
                placed_by=slot_placed_by(key),
                repos=sorted(repos or []),
                pursuits=pursuits.get(initiative.number, []),
                open_count=open_count,
            ),
        )

    for idea in ideas:
        key = readiness.record_sort_key(idea.number, idea.priority, idea.rank)
        place(
            key,
            schemas.QueueBoardRow(
                kind='idea',
                number=idea.number,
                title=idea.title,
                step='discuss',
                priority=idea.priority,
                own_priority=idea.priority,
                placed_by=slot_placed_by(key),
                pursuits=pursuits.get(idea.number, []),
            ),
        )

    return schemas.QueueBoard(read_ts=now, queue=placement.queue_rows(), claimed=placement.claimed_rows())
