"""Each day of a focus, with each pursuit's output that day and its standing over the window ending at the day's end.

`services/focus_standing.py` computes every figure. This module lays out the days and reads the
output once for all of them.

- **A day's window is a span of instants, as `focus_standing` opens its own.** It ends at the day's
  midnight in the request's zone, or where the focus stopped holding on its last day. It opens at
  the later of the focus's start and `USAGE_WINDOW` before that end, so a window across a change of
  clocks still holds 720 hours. The last day of the focus in force ends at the read, so its window
  is the standing's own.
- **One query reads every day's output.** Each window's and each day's bounds are breakpoints, and
  `width_bucket` sums each worklog's output between each pair of neighboring breakpoints. A window
  adds the stretches between its bounds. So each worklog's total in it is the same integer that a
  query over that window alone sums, and the figures agree to the last bit.
- **Every day reads the pursuits as they stand now.** The pursuits are the ones active now, and each
  record reaches the pursuits it reaches now. So an earlier day can differ from what `GET /focus`
  answered on that day.
"""

from __future__ import annotations

import datetime as dt
from bisect import bisect_left
from bisect import bisect_right
from collections import defaultdict
from collections.abc import Sequence
from dataclasses import asdict
from dataclasses import dataclass
from zoneinfo import ZoneInfo

import structlog
from sqlalchemy import DateTime
from sqlalchemy import Row
from sqlalchemy import bindparam
from sqlalchemy import func
from sqlalchemy import select
from sqlalchemy.dialects.postgresql import ARRAY
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.api.exceptions import NotFoundException
from slate.services.focus_standing import USAGE_WINDOW
from slate.services.focus_standing import DrawnOutput
from slate.services.focus_standing import WorklogOutput
from slate.services.focus_standing import filled_shares
from slate.services.focus_standing import furthest_under_first
from slate.services.focus_standing import listed
from slate.services.focus_standing import pursuit_figures
from slate.services.focus_standing import pursuits_drawing
from slate.services.focus_standing import pursuits_reached
from slate.services.focus_standing import split_output
from slate.services.focuses import focus_in_force
from slate.services.focuses import focus_view
from slate.services.focuses import in_force
from slate.services.time_window import TimeWindow
from slate.services.time_window import day_starts
from slate.services.time_window import last_day_before
from slate.services.work_totals import counted_messages
from slate.services.work_totals import in_worklog_spans

logger = structlog.get_logger()

# The most days one read answers. A focus longer than this answers its last ones.
USAGE_DAYS = 30


@dataclass(frozen=True)
class FocusDay:
    """One day of the read, with the window that holds its figures and the instant that starts its own output."""

    day: dt.date
    window_since: dt.datetime
    output_since: dt.datetime
    until: dt.datetime


def stopped_holding(focus: models.Focus, now: dt.datetime, today: dt.date, zone: ZoneInfo) -> dt.datetime:
    """When the focus stopped holding: its end, the midnight after its `until_date` where that passed, else `now`."""
    if focus.ended_ts is not None:
        return focus.ended_ts
    if in_force(focus, today):
        return now
    return day_starts(focus.until_date + dt.timedelta(days=1), zone)


def focus_days(started: dt.datetime | None, stopped: dt.datetime, zone: ZoneInfo) -> list[FocusDay]:
    """The last `USAGE_DAYS` days in `zone` from a focus's start to where it stopped holding.

    With no focus, `started` is None, and the days are the last `USAGE_DAYS` up to `stopped` with
    nothing bounding their windows' start.
    """
    first_instant = stopped - USAGE_WINDOW if started is None else started
    stopped = max(stopped, first_instant)
    last_day = last_day_before(stopped, zone) if stopped > first_instant else first_instant.astimezone(zone).date()
    first_day = max(first_instant.astimezone(zone).date(), last_day - dt.timedelta(days=USAGE_DAYS - 1))
    days = []
    for offset in range((last_day - first_day).days + 1):
        day = first_day + dt.timedelta(days=offset)
        until = min(day_starts(day + dt.timedelta(days=1), zone), stopped)
        opens, begins = until - USAGE_WINDOW, day_starts(day, zone)
        days.append(
            FocusDay(
                day=day,
                window_since=opens if started is None else max(started, opens),
                output_since=begins if started is None else max(started, begins),
                until=until,
            )
        )
    return days


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
    """The read's output by worklog and stretch, summed and split over any window between two breakpoints.

    Each worklog keeps its stretches in order and its running output across them, so a window's
    total is two bisections and a subtraction rather than a pass over every stretch.
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
        """Each worklog's output from `since` up to `until`, split across the pursuits its record reaches."""
        first, last = self.position[since] + 1, self.position[until]
        outputs = {}
        for worklog_id, stretches in self.stretches.items():
            start, stop = bisect_left(stretches, first), bisect_right(stretches, last)
            if stop > start:
                running = self.running[worklog_id]
                outputs[worklog_id] = WorklogOutput(self.records[worklog_id], running[stop] - running[start])
        return split_output(outputs, self.reached)


def focus_read(session: Session, focus_id: int | None, today: dt.date) -> models.Focus | None:
    """The focus `focus_id` names, else the one in force, else None."""
    if focus_id is None:
        return focus_in_force(session, today)
    focus = session.get(models.Focus, focus_id)
    if focus is None:
        raise NotFoundException('focus', focus_id, logger)
    return focus


def focus_usage_by_day(session: Session, now: dt.datetime, today: dt.date, zone_name: str, focus_id: int | None) -> schemas.FocusUsage:
    """Each day of the focus `focus_id` names, else the one in force, else the last `USAGE_DAYS`, as of `now`."""
    zone = ZoneInfo(zone_name)
    focus = focus_read(session, focus_id, today)
    stopped = (now if focus is None else stopped_holding(focus, now, today, zone)).astimezone(dt.UTC)
    days = focus_days(None if focus is None else focus.started_ts.astimezone(dt.UTC), stopped, zone)
    output = OutputByStretch(session, sorted({instant for day in days for instant in (day.window_since, day.output_since, day.until)}))
    drawn = [(day, output.drawn(day.window_since, day.until), output.drawn(day.output_since, day.until)) for day in days]
    pursuits = pursuits_drawing(session, {number for _, window, _ in drawn for number in window.output})
    shares = filled_shares(focus, [pursuit for pursuit in pursuits if pursuit.status == 'active'])
    answered = []
    for day, window, on_day in drawn:
        numbers = [pursuit.number for pursuit in listed(pursuits, window)]
        figures = pursuit_figures(window, numbers, shares)
        rows = [
            schemas.PursuitUsageOnDay(number=number, split_output_tokens_on_day=on_day.output.get(number, 0.0), **asdict(figures[number]))
            for number in numbers
        ]
        answered.append(
            schemas.FocusUsageDay(
                day=day.day,
                window=schemas.FocusWindow(since=day.window_since, until=day.until),
                pursuits=sorted(rows, key=furthest_under_first),
            )
        )
    named = {row.number for day in answered for row in day.pursuits}
    return schemas.FocusUsage(
        focus=None if focus is None else focus_view(focus, today),
        since=days[0].output_since if days else stopped,
        until=days[-1].until if days else stopped,
        pursuits=[
            schemas.FocusUsagePursuit(number=pursuit.number, name=pursuit.name, status=pursuit.status)
            for pursuit in pursuits
            if pursuit.number in named
        ],
        days=answered,
    )
