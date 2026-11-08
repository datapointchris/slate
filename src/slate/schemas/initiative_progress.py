"""An initiative's progress: each of its issues counted in one state, the work under way and next, its closes, and its totals."""

import datetime as dt
from typing import Literal
from typing import get_args

from slate.schemas.initiative import Initiative
from slate.schemas.issue import Issue
from slate.schemas.queue import QueueRow
from slate.schemas.record import RecordModel
from slate.schemas.work_totals import WorkTotals

# Each issue counts in exactly one of these. `issue_state` decides which, in its own order.
IssueState = Literal['completed', 'canceled', 'running', 'waiting', 'ready', 'blocked', 'triage', 'deferred', 'shelved', 'open_children']


class IssueStateCounts(RecordModel):
    """How many of the initiative's issues are in each state. The counts partition its `issue_count`.

    `running` and `waiting` are issues under a live claim, `waiting` where the holder parked it.
    `open_children` is an open issue nothing else holds back but its open children.
    """

    completed: int = 0
    canceled: int = 0
    running: int = 0
    waiting: int = 0
    ready: int = 0
    blocked: int = 0
    triage: int = 0
    deferred: int = 0
    shelved: int = 0
    open_children: int = 0


# A state the counts lack would fail only when an issue first reached it, so the two lists are held equal here.
if tuple(IssueStateCounts.model_fields) != get_args(IssueState):
    raise TypeError(f'IssueStateCounts holds {tuple(IssueStateCounts.model_fields)}, and IssueState names {get_args(IssueState)}')


class BurnupStep(RecordModel):
    """The initiative's issues from `ts` until the next step: `scope` held in it and not canceled, and `completed` of those."""

    ts: dt.datetime
    scope: int
    completed: int


class InitiativeProgress(RecordModel):
    """Everything a progress view prints for one initiative, in one read.

    `running`, `waiting` and `blocked` hold the issues in that state, in queue order. `next` is the
    head of `GET /queue` for the initiative. `closed` is every issue whose close falls between
    `closed_since` and `closed_until`, latest first, and a null bound leaves that end open. `totals`
    and `burnup` cover the initiative's whole life, whatever the window. `burnup` is oldest first,
    a step at each moment its scope or its completed issues changed, and a last at the read.
    """

    initiative: Initiative
    states: IssueStateCounts
    running: list[Issue]
    waiting: list[Issue]
    blocked: list[Issue]
    next: list[QueueRow]
    closed: list[Issue]
    closed_since: dt.datetime | None = None
    closed_until: dt.datetime | None = None
    totals: WorkTotals
    burnup: list[BurnupStep]
