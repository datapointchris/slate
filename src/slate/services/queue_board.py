"""Every open row of the queue, slim, for a board that polls it.

`services/work_queue.py` chooses each row, says where it sits and places it, so the board lists
`GET /queue`'s rows in `GET /queue`'s order. It reads only what each row draws, and builds no
`issue_views`, `initiative_views` or `idea_views` row, which join each record's refs, claim,
checks and steps. Its queries stay the same however many rows it answers.
"""

from __future__ import annotations

from sqlalchemy.orm import Session
from sqlalchemy.orm import load_only
from sqlalchemy.orm import selectinload

from slate import models
from slate import schemas
from slate.services.claim_views import live_claims_on_every_record
from slate.services.held_plans import held_plans
from slate.services.issue_paths import held_overlaps
from slate.services.issue_readiness import measure_readiness
from slate.services.issue_views import calendar_now
from slate.services.record_pursuits import issue_pursuits
from slate.services.record_pursuits import pursuits_of
from slate.services.record_steps import initiative_step_views
from slate.services.work_queue import QueuePlacement
from slate.services.work_queue import QueueSpot
from slate.services.work_queue import idea_spot
from slate.services.work_queue import initiative_row_step
from slate.services.work_queue import issue_spot
from slate.services.work_queue import open_ideas
from slate.services.work_queue import open_initiative_rows
from slate.services.work_queue import path_overlaps
from slate.services.work_queue import queued_issues
from slate.services.work_queue import record_spot

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
    selectinload(models.Issue.initiative).load_only(
        models.Initiative.number,
        models.Initiative.name,
        models.Initiative.status,
        models.Initiative.priority,
        models.Initiative.autofix,
    ),
)
INITIATIVE_COLUMNS = load_only(models.Initiative.number, models.Initiative.name, models.Initiative.priority, models.Initiative.rank)
IDEA_COLUMNS = load_only(models.Idea.number, models.Idea.title, models.Idea.priority, models.Idea.rank)


def queue_board(session: Session, zone: str) -> schemas.QueueBoard:
    """The queue as of now, every row slim, its deferral days read in `zone`."""
    today, now = calendar_now(zone)
    readiness = measure_readiness(session, today, now)
    held = live_claims_on_every_record(session, now)
    placement: QueuePlacement[schemas.QueueBoardRow] = QueuePlacement(held, held_plans(session, held))

    def place(spot: QueueSpot, row: schemas.QueueBoardRow) -> None:
        claim = placement.place(spot, row)
        if claim is not None:
            wait = claim.open_wait
            row.expires_ts = claim.expires_ts
            row.waiting = None if wait is None else schemas.ClaimWait.model_validate(wait)

    every_row = schemas.QueueFilter.model_validate({})
    issues = queued_issues(session, readiness, every_row, None, held, options=ISSUE_COLUMNS)
    overlaps = held_overlaps(session, issues, now)
    pursuits_of_issue = issue_pursuits(session, issues)
    for issue in issues:
        spot = issue_spot(readiness, issue)
        place(
            spot,
            schemas.QueueBoardRow(
                **spot.row_fields(),
                kind='issue',
                number=issue.number,
                title=issue.title,
                own_priority=issue.priority,
                initiative=None if issue.initiative is None else schemas.InitiativeSummary.model_validate(issue.initiative),
                repos=[] if issue.repo is None else [issue.repo],
                pursuits=pursuits_of_issue[issue.number],
                overlaps=path_overlaps(overlaps.get(issue.number, [])),
            ),
        )

    initiatives = open_initiative_rows(session, None, every_row, INITIATIVE_COLUMNS)
    ideas = open_ideas(session, every_row, None, IDEA_COLUMNS)
    steps = initiative_step_views(session, [initiative for initiative, _ in initiatives])
    pursuits = pursuits_of(session, [*(initiative.number for initiative, _ in initiatives), *(idea.number for idea in ideas)])

    for initiative, counts in initiatives:
        step = initiative_row_step(initiative.number, steps[initiative.number], counts.open_count, readiness, held)
        if step is None:
            continue
        spot = record_spot(readiness, initiative.number, initiative.priority, initiative.rank, step)
        place(
            spot,
            schemas.QueueBoardRow(
                **spot.row_fields(),
                kind='initiative',
                number=initiative.number,
                title=initiative.name,
                own_priority=initiative.priority,
                repos=sorted(counts.repos or []),
                pursuits=pursuits.get(initiative.number, []),
                open_count=counts.open_count,
            ),
        )

    for idea in ideas:
        spot = idea_spot(readiness, idea)
        place(
            spot,
            schemas.QueueBoardRow(
                **spot.row_fields(),
                kind='idea',
                number=idea.number,
                title=idea.title,
                own_priority=idea.priority,
                pursuits=pursuits.get(idea.number, []),
            ),
        )

    return schemas.QueueBoard(read_ts=now, queue=placement.queue_rows(), claimed=placement.claimed_rows())
