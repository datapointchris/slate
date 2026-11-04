"""Narrowing a list read to the rows dated inside a window of instants.

A window is half-open. `since` is the first instant inside it and `until` the
first instant past it, so a day's window is its first instant to the next day's
and a row stamped at midnight belongs to one day only. Either bound narrows on
its own, which leaves the other end open.

The client resolves a span, a day or an instant to an instant before it calls,
so no duration and no bare day reaches this module. The filter runs in the
query rather than in each client, so every client shares one meaning of a
window.

A row with no date compares as null, so it is outside every window: an open
issue was not "closed this week".
"""

import datetime as dt
from dataclasses import dataclass
from zoneinfo import ZoneInfo

import sqlalchemy as sa
from sqlalchemy import Select
from sqlalchemy.orm import InstrumentedAttribute


@dataclass(frozen=True)
class TimeWindow:
    since: dt.datetime | None = None
    until: dt.datetime | None = None

    def holds(self, moment: dt.datetime | None) -> bool:
        """Whether `moment` is inside the window, for rows already read, as `apply_time_window` narrows a timestamp column."""
        return moment is not None and (self.since is None or moment >= self.since) and (self.until is None or moment < self.until)


def is_date_column(column: InstrumentedAttribute) -> bool:
    return isinstance(column.type, sa.Date) and not isinstance(column.type, sa.DateTime)


def last_day_before(until: dt.datetime, zone: ZoneInfo) -> dt.date:
    """The last calendar day a window ending at `until` reaches into.

    A window ending at midnight stops before that day begins. One ending at any
    later moment reaches into the day it ends on.
    """
    local = until.astimezone(zone)
    return local.date() - dt.timedelta(days=1) if local.time() == dt.time.min else local.date()


def apply_time_window(query: Select, column: InstrumentedAttribute, window: TimeWindow, *, zone: str | None) -> Select:
    """Narrow to rows whose `column` falls inside `window`, or leave the query alone.

    A `Date` column holds a calendar day, which is a span of instants. A day is
    kept when that span overlaps the window, read in `zone`. Comparing a date to
    an aware instant instead would make Postgres cast in the session zone and
    lose a day at each end west of UTC. A timestamp column takes no zone, so
    its call passes `None`.
    """
    if is_date_column(column):
        if zone is None:
            raise TypeError(f'{column} stores calendar days, so a window on it needs a zone')
        tz = ZoneInfo(zone)
        if window.since is not None:
            query = query.where(column >= window.since.astimezone(tz).date())
        if window.until is not None:
            query = query.where(column <= last_day_before(window.until, tz))
        return query
    if window.since is not None:
        query = query.where(column >= window.since)
    if window.until is not None:
        query = query.where(column < window.until)
    return query
