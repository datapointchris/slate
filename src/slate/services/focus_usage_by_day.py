"""Each day of a focus, with each pursuit's output that day and its standing over the window ending at the day's end.

`services/focus_standing.py` computes every figure. This module lays out the days and reads the
output once for all of them.

- **A day's window is `usage_window` ending at the day's end.** That end is the day's midnight in
  the request's zone, or where the focus stopped holding on its last day, as `held_until` reads it.
  The window is a span of instants, so one across a change of clocks holds `USAGE_WINDOW` of
  elapsed time. The last day of the focus in force ends at the read, so its window is the
  standing's own.
- **One query reads every day's output.** Each window's and each day's bounds are breakpoints for
  `OutputByStretch`, which `GET /focus` reads through with its window's two bounds. So each
  worklog's total in a window is the same integer for both reads, and the figures agree to the
  last bit.
- **Every day reads the pursuits as they stand now.** A day lists the pursuits active now and any
  other that drew output in its window. Each record reaches the pursuits it reaches now. So an
  earlier day can differ from what `GET /focus` answered on that day.
"""

from __future__ import annotations

import datetime as dt
from dataclasses import dataclass
from zoneinfo import ZoneInfo

import structlog
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.api.exceptions import NotFoundException
from slate.services.focus_standing import USAGE_WINDOW
from slate.services.focus_standing import OutputByStretch
from slate.services.focus_standing import filled_shares
from slate.services.focus_standing import furthest_under_first
from slate.services.focus_standing import listed
from slate.services.focus_standing import pursuit_figures
from slate.services.focus_standing import pursuits_listed
from slate.services.focus_standing import usage_window
from slate.services.focuses import focus_in_force
from slate.services.focuses import focus_view
from slate.services.focuses import held_until
from slate.services.time_window import day_starts
from slate.services.time_window import last_day_before

logger = structlog.get_logger()

# The most days one read answers. A focus longer than this answers its last ones.
USAGE_DAYS = 30


@dataclass(frozen=True)
class FocusDay:
    """One day of the read, with the window that holds its figures and the instant from which its own output counts."""

    day: dt.date
    window: schemas.FocusWindow
    output_since: dt.datetime


def focus_days(started: dt.datetime | None, stopped: dt.datetime, zone: ZoneInfo) -> list[FocusDay]:
    """The last `USAGE_DAYS` days in `zone` from a focus's start to where it stopped holding, both UTC.

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
        begins = day_starts(day, zone)
        output_since = begins if started is None else max(started, begins)
        days.append(FocusDay(day=day, window=usage_window(started, until), output_since=output_since))
    return days


def focus_read(session: Session, focus_id: int | None, now: dt.datetime, zone: str) -> models.Focus | None:
    """The focus `focus_id` names, else the one in force, else None."""
    if focus_id is None:
        return focus_in_force(session, now, zone)
    focus = session.get(models.Focus, focus_id)
    if focus is None:
        raise NotFoundException('focus', focus_id, logger)
    return focus


def focus_usage_by_day(session: Session, now: dt.datetime, zone: str, focus_id: int | None) -> schemas.FocusUsage:
    """Each day of the focus `focus_id` names, else the one in force, else the last `USAGE_DAYS`, as of `now`."""
    focus = focus_read(session, focus_id, now, zone)
    now = now.astimezone(dt.UTC)
    stopped = now if focus is None else min(held_until(focus, zone), now)
    days = focus_days(None if focus is None else focus.started_ts.astimezone(dt.UTC), stopped, ZoneInfo(zone))
    output = OutputByStretch(session, sorted({bound for day in days for bound in (day.window.since, day.output_since, day.window.until)}))
    drawn = [(day, output.drawn(day.window.since, day.window.until), output.drawn(day.output_since, day.window.until)) for day in days]
    pursuits = pursuits_listed(session, {number for _, window, _ in drawn for number in window.output})
    shares = filled_shares(focus, pursuits)
    answered = []
    for day, window, on_day in drawn:
        numbers = [pursuit.number for pursuit in pursuits if listed(pursuit, window.output)]
        figures = pursuit_figures(window, numbers, shares)
        rows = [
            schemas.PursuitUsageOnDay(**figures[number].model_dump(), split_output_tokens_on_day=on_day.output.get(number, 0.0))
            for number in numbers
        ]
        answered.append(
            schemas.FocusUsageDay(
                day=day.day,
                window=day.window,
                pursuits=sorted(rows, key=furthest_under_first),
            )
        )
    named = {row.number for day in answered for row in day.pursuits}
    days_in_focus = None
    if focus is not None and days:
        days_in_focus = (days[-1].day - focus.started_ts.astimezone(ZoneInfo(zone)).date()).days + 1
    return schemas.FocusUsage(
        focus=None if focus is None else focus_view(focus, now, zone),
        since=days[0].output_since if days else stopped,
        until=days[-1].window.until if days else stopped,
        days_in_focus=days_in_focus,
        pursuits=[
            schemas.FocusUsagePursuit(number=pursuit.number, name=pursuit.name, status=pursuit.status)
            for pursuit in pursuits
            if pursuit.number in named
        ],
        days=answered,
    )
