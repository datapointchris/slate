"""Where each pursuit stands against the focus in force: its share, the work it drew, and how far under or over its share that is.

Nothing here is stored. Each read derives every figure from the focus, the pursuits, the worklogs
and their messages, so a reader takes slate's numbers rather than working them out again.
`services/focus_usage_by_day.py` reads each day of a focus through the same functions.

- **Shares fill to 1.** A share the focus named for an active pursuit is taken as given. The active
  pursuits it left unnamed split the rest of 1 evenly. A named pursuit that is no longer active
  leaves, and its part goes to the unnamed ones.
- **Usage is output tokens.** `usage_window` opens the window at the later of the focus's start and
  `USAGE_WINDOW` before its end, which is the read here. Each worklog's output in it splits evenly
  across the pursuits its record reaches: an idea's or an initiative's own, an issue's
  initiative's. A pursuit's `usage` is its part of the output every pursuit drew.
- **The factor is `2 ** (-usage / share)`.** It reads 0.5 at the share, toward 1 under it and
  toward 0 over it. A share of 0 reads 0, because any work at all is over it.
- **The last touch** is the latest on any initiative in the pursuit, as `last_touched` reads one
  for the live board.
"""

from __future__ import annotations

import datetime as dt
from bisect import bisect_left
from bisect import bisect_right
from collections import defaultdict
from collections.abc import Collection
from collections.abc import Mapping
from collections.abc import Sequence
from dataclasses import dataclass
from dataclasses import field
from decimal import Decimal
from uuid import UUID

from sqlalchemy import DateTime
from sqlalchemy import Row
from sqlalchemy import bindparam
from sqlalchemy import func
from sqlalchemy import select
from sqlalchemy.dialects.postgresql import ARRAY
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


def usage_window(started: dt.datetime | None, until: dt.datetime) -> schemas.FocusWindow:
    """The window usage is counted over, ending at `until`: from the later of the focus's start and `USAGE_WINDOW` before.

    `started` is None where no focus is read. Both bounds are UTC, so the window holds
    `USAGE_WINDOW` of elapsed time across a change of clocks.
    """
    until = until.astimezone(dt.UTC)
    opens = until - USAGE_WINDOW
    return schemas.FocusWindow(since=opens if started is None else max(started.astimezone(dt.UTC), opens), until=until)


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


def output_by_stretch(session: Session, breakpoints: Sequence[dt.datetime]) -> Sequence[Row]:
    """Each worklog's output between each pair of neighboring breakpoints, which must be sorted.

    A row's `stretch` is `width_bucket`'s number. Stretch n is what was written from
    `breakpoints[n - 1]` up to `breakpoints[n]`. A worklog with no message in a stretch has no row
    for it.
    """
    thresholds = bindparam('thresholds', list(breakpoints), type_=ARRAY(DateTime(timezone=True)))
    placed = in_worklog_spans(
        select(
            models.WorklogSpan.worklog_id,
            models.Worklog.record_number,
            func.width_bucket(models.MessageTokens.written_ts, thresholds).label('stretch'),
            models.MessageTokens.output_tokens,
        )
    ).join(models.Worklog, models.Worklog.id == models.WorklogSpan.worklog_id)
    counted = counted_messages(placed, None, TimeWindow(since=breakpoints[0], until=breakpoints[-1])).subquery()
    return session.execute(
        select(counted.c.worklog_id, counted.c.record_number, counted.c.stretch, func.sum(counted.c.output_tokens).label('output'))
        .group_by(counted.c.worklog_id, counted.c.record_number, counted.c.stretch)
        .order_by(counted.c.worklog_id, counted.c.stretch)
    ).all()


class OutputByStretch:
    """Output by worklog and stretch, read once, then summed and split over any window between two breakpoints.

    Each worklog keeps its stretches in order and its running output across them, so a window's
    total is two bisections and a subtraction rather than a pass over every stretch. That total is
    the same integer that a query over the window alone sums.
    """

    def __init__(self, session: Session, breakpoints: Sequence[dt.datetime]):
        rows = output_by_stretch(session, breakpoints) if len(breakpoints) > 1 else []
        self.position = {instant: index for index, instant in enumerate(breakpoints)}
        self.records: dict[int, int] = {}
        self.stretches: dict[int, list[int]] = defaultdict(list)
        self.running: dict[int, list[int]] = defaultdict(lambda: [0])
        for row in rows:
            self.records[row.worklog_id] = row.record_number
            self.stretches[row.worklog_id].append(row.stretch)
            running = self.running[row.worklog_id]
            running.append(running[-1] + row.output)
        self.reached = pursuits_reached(session, set(self.records.values()))

    def drawn(self, since: dt.datetime, until: dt.datetime) -> DrawnOutput:
        """Each worklog's output from `since` up to `until`, split across the pursuits its record reaches. Both are breakpoints."""
        first, last = self.position[since] + 1, self.position[until]
        outputs = {}
        for worklog_id, stretches in self.stretches.items():
            start, stop = bisect_left(stretches, first), bisect_right(stretches, last)
            if stop > start:
                running = self.running[worklog_id]
                outputs[worklog_id] = WorklogOutput(self.records[worklog_id], running[stop] - running[start])
        return split_output(outputs, self.reached)


def drawn_output(session: Session, window: schemas.FocusWindow) -> DrawnOutput:
    """Each worklog's output in the window, split evenly across the pursuits its record reaches."""
    return OutputByStretch(session, [window.since, window.until]).drawn(window.since, window.until)


def listed(pursuit: models.Pursuit, drawing: Collection[int]) -> bool:
    """Whether a read lists the pursuit: it is active, or `drawing`, the pursuits that drew output in the window, holds it."""
    return pursuit.status == 'active' or pursuit.number in drawing


def pursuits_listed(session: Session, drawing: Collection[int]) -> list[models.Pursuit]:
    """Each pursuit that `listed` keeps, in number order."""
    return [pursuit for pursuit in session.scalars(select(models.Pursuit).order_by(models.Pursuit.number)) if listed(pursuit, drawing)]


def filled_shares(focus: models.Focus | None, pursuits: Sequence[models.Pursuit]) -> dict[int, tuple[Decimal, bool]]:
    """Each active pursuit's share by number, and whether the focus named it. Empty where no focus is in force."""
    if focus is None:
        return {}
    named = {share.pursuit.number: share.share for share in focus.shares if share.pursuit.status == 'active'}
    unnamed = [pursuit.number for pursuit in pursuits if pursuit.status == 'active' and pursuit.number not in named]
    rest = max(Decimal(1) - sum(named.values(), Decimal(0)), Decimal(0))
    shares = {number: (share, True) for number, share in named.items()}
    shares |= {number: (rest / len(unnamed), False) for number in unnamed}
    return shares


def factor(usage: float, share: Decimal) -> float:
    return 0.0 if share == 0 else 2 ** (-usage / float(share))


def pursuit_figures(
    drawn: DrawnOutput, numbers: Collection[int], shares: Mapping[int, tuple[Decimal, bool]]
) -> dict[int, schemas.PursuitFigures]:
    """Each pursuit's figures over the window `drawn` covers, by its number."""
    attributed = sum(drawn.output.values())
    figures = {}
    for number in numbers:
        output = drawn.output.get(number, 0.0)
        usage = output / attributed if attributed else 0.0
        share, named = shares.get(number, (None, False))
        figures[number] = schemas.PursuitFigures(
            number=number,
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


def furthest_under_first(row: schemas.PursuitFigures) -> tuple:
    return (row.factor is None, -(row.factor or 0), row.split_output_tokens, row.number)


def focus_standing(session: Session, now: dt.datetime, zone: str) -> schemas.FocusStanding:
    """The focus in force at `now`, and each pursuit's standing against it over the window ending `now`."""
    focus = focus_in_force(session, now, zone)
    window = usage_window(None if focus is None else focus.started_ts, now)
    drawn = drawn_output(session, window)
    pursuits = pursuits_listed(session, drawn.output)
    shares = filled_shares(focus, pursuits)
    touched = pursuits_touched(session, [pursuit.id for pursuit in pursuits])
    figures = pursuit_figures(drawn, [pursuit.number for pursuit in pursuits], shares)
    rows = [
        schemas.PursuitStanding(
            **figures[pursuit.number].model_dump(), name=pursuit.name, status=pursuit.status, touched_ts=touched.get(pursuit.id)
        )
        for pursuit in pursuits
    ]
    outside = sum(row.output for row in message_rows(session, None, TimeWindow(since=window.since, until=window.until)) if row.outside)
    return schemas.FocusStanding(
        focus=None if focus is None else focus_view(focus, now, zone),
        window=window,
        pursuits=sorted(rows, key=furthest_under_first),
        unattributed=schemas.UnattributedUsage(
            output_tokens=drawn.unattributed_output,
            worklog_count=drawn.unattributed_worklogs,
            outside_worklogs_output_tokens=outside,
        ),
    )
