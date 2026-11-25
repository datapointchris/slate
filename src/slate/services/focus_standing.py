"""Where each pursuit stands against the focus in force: its share, the work it drew, and how far under or over its share that is.

Nothing here is stored. Each read derives every figure from the focus, the pursuits, the worklogs
and their messages, so a reader takes slate's numbers rather than working them out again.

- **Shares fill to 1.** A share the focus named for an active pursuit is taken as given. The active
  pursuits it left unnamed split the rest of 1 evenly. A named pursuit that is no longer active
  leaves, and its part goes to the unnamed ones.
- **Usage is output tokens.** The window opens at the later of the focus's start and `USAGE_WINDOW`
  before the read. Each worklog's output in it splits evenly across the pursuits its record
  reaches: an idea's or an initiative's own, an issue's initiative's. A pursuit's `usage` is its
  part of the output every pursuit drew.
- **The factor is `2 ** (-usage / share)`.** It reads 0.5 at the share, toward 1 under it and
  toward 0 over it. A share of 0 reads 0, because any work at all is over it.
- **The last touch** is the latest on any initiative in the pursuit, as `last_touched` reads one
  for the live board.
"""

from __future__ import annotations

import datetime as dt
from collections import defaultdict
from collections.abc import Collection
from collections.abc import Mapping
from collections.abc import Sequence
from dataclasses import asdict
from dataclasses import dataclass
from dataclasses import field
from decimal import Decimal
from uuid import UUID

from sqlalchemy import func
from sqlalchemy import or_
from sqlalchemy import select
from sqlalchemy.orm import Session
from sqlalchemy.orm import defer
from sqlalchemy.orm import load_only
from sqlalchemy.orm import selectinload

from slate import models
from slate import schemas
from slate.services.focuses import focus_in_force
from slate.services.focuses import focus_view
from slate.services.initiative_board import last_touched
from slate.services.record_pursuits import issue_pursuits
from slate.services.record_pursuits import pursuits_of
from slate.services.time_window import TimeWindow
from slate.services.work_totals import counted_messages
from slate.services.work_totals import in_worklog_spans
from slate.services.work_totals import message_rows

USAGE_WINDOW = dt.timedelta(days=30)


@dataclass
class DrawnOutput:
    """The window's output tokens by pursuit number, and what reached none."""

    output: dict[int, float] = field(default_factory=lambda: defaultdict(float))
    worklogs: dict[int, int] = field(default_factory=lambda: defaultdict(int))
    unattributed_output: int = 0
    unattributed_worklogs: int = 0


def pursuits_reached(session: Session, numbers: Collection[int]) -> dict[int, list[int]]:
    """The numbers of the pursuits each record reaches, by its number. A record reaching none is left out."""
    issues = session.scalars(
        select(models.Issue)
        .options(load_only(models.Issue.number, models.Issue.initiative_id))
        .options(selectinload(models.Issue.initiative).load_only(models.Initiative.number))
        .where(models.Issue.number.in_(list(numbers)))
    ).all()
    reached = {number: [pursuit.number for pursuit in held] for number, held in issue_pursuits(session, issues).items() if held}
    others = [number for number in numbers if number not in {issue.number for issue in issues}]
    reached |= {number: [pursuit.number for pursuit in held] for number, held in pursuits_of(session, others).items()}
    return reached


@dataclass(frozen=True)
class WorklogOutput:
    """One worklog's output tokens in a window, and the record it is on."""

    record_number: int
    output: int


def split_output(outputs: Mapping[int, WorklogOutput], reached: Mapping[int, list[int]]) -> DrawnOutput:
    """Each worklog's output, by worklog id, split evenly across the pursuits its record reaches.

    The worklogs go in id order, so two reads holding the same worklogs add each pursuit's parts in
    the same order and agree to the last bit.
    """
    drawn = DrawnOutput()
    for worklog_id in sorted(outputs):
        worklog = outputs[worklog_id]
        pursuits = reached.get(worklog.record_number, [])
        if not pursuits:
            drawn.unattributed_output += worklog.output
            drawn.unattributed_worklogs += 1
        for number in pursuits:
            drawn.output[number] += worklog.output / len(pursuits)
            drawn.worklogs[number] += 1
    return drawn


def drawn_output(session: Session, window: TimeWindow) -> DrawnOutput:
    """Each worklog's output in the window, split evenly across the pursuits its record reaches."""
    output = func.coalesce(func.sum(models.MessageTokens.output_tokens), 0).label('output')
    query = in_worklog_spans(select(models.WorklogSpan.worklog_id, models.Worklog.record_number, output)).join(
        models.Worklog, models.Worklog.id == models.WorklogSpan.worklog_id
    )
    rows = session.execute(
        counted_messages(query, None, window).group_by(models.WorklogSpan.worklog_id, models.Worklog.record_number)
    ).all()
    reached = pursuits_reached(session, {row.record_number for row in rows})
    return split_output({row.worklog_id: WorklogOutput(row.record_number, row.output) for row in rows}, reached)


def filled_shares(focus: models.Focus | None, active: Sequence[models.Pursuit]) -> dict[int, tuple[Decimal, bool]]:
    """Each active pursuit's share by number, and whether the focus named it. Empty where no focus is in force."""
    if focus is None:
        return {}
    named = {share.pursuit.number: share.share for share in focus.shares if share.pursuit.status == 'active'}
    unnamed = [pursuit.number for pursuit in active if pursuit.number not in named]
    rest = max(Decimal(1) - sum(named.values(), Decimal(0)), Decimal(0))
    shares = {number: (share, True) for number, share in named.items()}
    shares |= {number: (rest / len(unnamed), False) for number in unnamed}
    return shares


def factor(usage: float, share: Decimal) -> float:
    return 0.0 if share == 0 else 2 ** (-usage / float(share))


@dataclass(frozen=True)
class PursuitFigures:
    """One pursuit's share, output, usage and factor over one window, as `schemas.PursuitStanding` gives them."""

    share: float | None
    share_named: bool
    split_output_tokens: float
    worklog_count: int
    usage: float
    factor: float | None


def listed(pursuits: Sequence[models.Pursuit], drawn: DrawnOutput) -> list[models.Pursuit]:
    """The active pursuits, and every other one that drew output in the window."""
    return [pursuit for pursuit in pursuits if pursuit.status == 'active' or pursuit.number in drawn.output]


def pursuit_figures(drawn: DrawnOutput, numbers: Collection[int], shares: Mapping[int, tuple[Decimal, bool]]) -> dict[int, PursuitFigures]:
    """Each pursuit's figures over the window `drawn` covers, by its number."""
    attributed = sum(drawn.output.values())
    figures = {}
    for number in numbers:
        output = drawn.output.get(number, 0.0)
        usage = output / attributed if attributed else 0.0
        share, named = shares.get(number, (None, False))
        figures[number] = PursuitFigures(
            share=None if share is None else float(share),
            share_named=named,
            split_output_tokens=output,
            worklog_count=drawn.worklogs.get(number, 0),
            usage=usage,
            factor=None if share is None else factor(usage, share),
        )
    return figures


def pursuits_touched(session: Session, pursuit_ids: Collection[UUID]) -> dict[UUID, dt.datetime]:
    """The latest touch on any initiative in each pursuit, by the pursuit's id. A pursuit holding none is left out."""
    placed = session.execute(
        select(models.RecordPursuit.pursuit_id, models.Initiative)
        .join(models.Initiative, models.Initiative.number == models.RecordPursuit.record_number)
        .options(defer(models.Initiative.description))
        .where(models.RecordPursuit.pursuit_id.in_(list(pursuit_ids)))
    ).all()
    touched = last_touched(session, list({initiative.id: initiative for _, initiative in placed}.values()))
    latest: dict[UUID, dt.datetime] = {}
    for pursuit_id, initiative in placed:
        moment = touched[initiative.number]
        latest[pursuit_id] = max(latest.get(pursuit_id, moment), moment)
    return latest


def furthest_under_first(row: schemas.PursuitStanding | schemas.PursuitUsageOnDay) -> tuple:
    return (row.factor is None, -(row.factor or 0), row.split_output_tokens, row.number)


def pursuits_drawing(session: Session, numbers: Collection[int]) -> Sequence[models.Pursuit]:
    """Every active pursuit and every pursuit `numbers` names, in number order."""
    return session.scalars(
        select(models.Pursuit)
        .where(or_(models.Pursuit.status == 'active', models.Pursuit.number.in_(list(numbers))))
        .order_by(models.Pursuit.number)
    ).all()


def focus_standing(session: Session, now: dt.datetime, today: dt.date) -> schemas.FocusStanding:
    """The focus in force on `today`, and each pursuit's standing against it over the window ending `now`."""
    focus = focus_in_force(session, today)
    since = now - USAGE_WINDOW if focus is None else max(focus.started_ts, now - USAGE_WINDOW)
    window = TimeWindow(since=since, until=now)
    drawn = drawn_output(session, window)
    pursuits = pursuits_drawing(session, drawn.output)
    shares = filled_shares(focus, [pursuit for pursuit in pursuits if pursuit.status == 'active'])
    touched = pursuits_touched(session, [pursuit.id for pursuit in pursuits])
    figures = pursuit_figures(drawn, [pursuit.number for pursuit in pursuits], shares)
    rows = [
        schemas.PursuitStanding(
            number=pursuit.number,
            name=pursuit.name,
            status=pursuit.status,
            touched_ts=touched.get(pursuit.id),
            **asdict(figures[pursuit.number]),
        )
        for pursuit in pursuits
    ]
    outside = sum(row.output for row in message_rows(session, None, window) if row.outside)
    return schemas.FocusStanding(
        focus=None if focus is None else focus_view(focus, today),
        window=schemas.FocusWindow(since=since, until=now),
        pursuits=sorted(rows, key=furthest_under_first),
        unattributed=schemas.UnattributedUsage(
            output_tokens=drawn.unattributed_output,
            worklog_count=drawn.unattributed_worklogs,
            outside_worklogs_output_tokens=outside,
        ),
    )
