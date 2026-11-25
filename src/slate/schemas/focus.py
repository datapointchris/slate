import datetime as dt
from decimal import Decimal
from typing import Annotated

from pydantic import BaseModel
from pydantic import ConfigDict
from pydantic import Field
from pydantic import StringConstraints

from slate.schemas.pursuit import PursuitSummary
from slate.schemas.write_effects import Written

Fraction = Annotated[Decimal, Field(ge=0, le=1, description='A fraction of the work, from 0 to 1')]
Statement = Annotated[str, StringConstraints(strip_whitespace=True, min_length=1)]


class FocusShareSet(BaseModel):
    """The share of the work a focus names for one pursuit, by the pursuit's number."""

    model_config = ConfigDict(extra='forbid')

    pursuit: int
    share: Fraction


class FocusSet(BaseModel):
    """A new focus, which ends the one in force.

    `until_date` is the last day it holds, today or later in the request's `timezone`. `shares` names
    active pursuits at most once each, summing to 1 at most. The active pursuits it leaves out split
    the rest of 1 evenly on read.
    """

    model_config = ConfigDict(extra='forbid')

    statement: Statement
    until_date: dt.date
    shares: list[FocusShareSet] = []


class FocusEnd(BaseModel):
    model_config = ConfigDict(extra='forbid')

    reason: Statement


class FocusShare(BaseModel):
    """A share as the focus named it."""

    pursuit: PursuitSummary
    share: float


class Focus(BaseModel):
    """A focus as it was set, and whether it is in force.

    `active` is derived on read: nothing ended it, and its `until_date` is today or later in the
    request's `timezone`. `shares` holds the shares it named, in pursuit number order.
    """

    id: int
    statement: str
    started_ts: dt.datetime
    until_date: dt.date
    ended_ts: dt.datetime | None = None
    ended_reason: str | None = None
    set_by: str
    active: bool
    shares: list[FocusShare] = []


class FocusWritten(Written):
    """The focus the write set or ended. Null where an end found none to end."""

    focus: Focus | None


class FocusWindow(BaseModel):
    """The window usage is counted over.

    It opens at the later of the focus's start and `USAGE_WINDOW` in `services/focus_standing.py`
    before `until`. The standing's window ends at the read. A usage day's ends at that day's
    midnight, or where the focus stopped holding on its last day.
    """

    since: dt.datetime
    until: dt.datetime


class PursuitFigures(BaseModel):
    """One pursuit's share, output, usage and factor over one window.

    `split_output_tokens` is the pursuit's part of the output of every worklog in the window whose
    record reaches it, each worklog's split evenly across the pursuits it reaches, so it may hold a
    fraction. `usage` is that over the output every pursuit drew, so the pursuits' usages sum to 1
    where any drew some.

    `share` is null where no focus is in force, or the pursuit is not active. `share_named` says
    the focus named it, rather than the pursuit taking its part of what the named shares left.
    `factor` is `2 ** (-usage / share)`: 0.5 at its share, toward 1 under it and toward 0 over it,
    and 0 where its share is 0. It is null where `share` is.
    """

    number: int
    share: float | None
    share_named: bool
    split_output_tokens: float
    worklog_count: int
    usage: float
    factor: float | None


class PursuitStanding(PursuitFigures):
    """Where one pursuit stands against its share of the work, with its name and status now.

    `touched_ts` is the latest `touched_ts` of any initiative in the pursuit, as the live board reads
    it. Null where the pursuit holds no initiative.
    """

    name: str
    status: str
    touched_ts: dt.datetime | None


class UnattributedUsage(BaseModel):
    """The window's output that reached no pursuit.

    `output_tokens` and `worklog_count` are the worklogs whose record reaches none.
    `outside_worklogs_output_tokens` is what sessions spent while in no worklog.
    """

    output_tokens: int
    worklog_count: int
    outside_worklogs_output_tokens: int


class FocusStanding(BaseModel):
    """The focus in force, and each pursuit's standing against it, as of `window.until`.

    `focus` is null where none is in force. `pursuits` holds every active pursuit and every other
    one that drew output in the window, the furthest under its share first.
    """

    focus: Focus | None
    window: FocusWindow
    pursuits: list[PursuitStanding]
    unattributed: UnattributedUsage


class FocusUsagePursuit(PursuitSummary):
    """A pursuit that some day of a usage read lists, with its status now."""

    status: str


class PursuitUsageOnDay(PursuitFigures):
    """One pursuit's figures over one day's window.

    `split_output_tokens_on_day` is its part of the output written that day while the focus held.
    """

    split_output_tokens_on_day: float


class FocusUsageDay(BaseModel):
    """One day of a focus, and each pursuit's figures over the window ending at its end.

    `window` ends at the day's midnight in the request's `timezone`, or where the focus stopped
    holding on its last day: the read, its end, or its `until_date` passing. `pursuits` lists every
    pursuit active now and every other one that drew output in the window, the furthest under its
    share first.
    """

    day: dt.date
    window: FocusWindow
    pursuits: list[PursuitUsageOnDay]


class FocusUsage(BaseModel):
    """Each day of a focus, oldest first, with each pursuit's output, usage, share and factor.

    `focus` is null where the read named none and none is in force, and the days are then the last
    `USAGE_DAYS` in `services/focus_usage_by_day.py`, with null shares. A focus longer than that
    answers its last `USAGE_DAYS`. `since` and `until` bound the days answered, inside the focus.
    `pursuits` names each pursuit that any day lists, in number order.

    Each day's figures read the pursuits active now, and place each record in its pursuits as they
    stand now. So only the last day of the focus in force is sure to equal `GET /focus`.
    """

    focus: Focus | None
    since: dt.datetime
    until: dt.datetime
    pursuits: list[FocusUsagePursuit]
    days: list[FocusUsageDay]
