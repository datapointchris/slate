"""Every repo's issues on one board, in a read cheap enough to poll."""

import datetime as dt

from slate.schemas.initiative_board import BoardCounts
from slate.schemas.record import Priority
from slate.schemas.record import RecordModel


class ReadyIssue(RecordModel):
    """An issue ready to take, and when it was filed, which is how long it has waited."""

    number: int
    title: str
    effective_priority: Priority
    created_ts: dt.datetime


class RepoRollup(BoardCounts):
    """A set of issues' counts and closes, with `closes_by_week` holding one count per `RepoBoard.weeks`, in that order."""

    closes_by_week: list[int]


class BoardRepo(RepoRollup):
    """One repo's issues, or every issue on no repo where `repo` is null. `ready` is its ready issues, the longest waiting first."""

    repo: str | None
    ready: list[ReadyIssue]


class RepoBoard(RecordModel):
    """The board as of `read_ts`, its calendar in `zone`, oldest first.

    `days` are the days each `closes.by_day` counts, and `weeks` the Monday each `closes_by_week`
    count starts on, the last the week of the read. `totals` is every issue. `repos` holds every repo
    an issue names, by name, then the issues on no repo where there are any.
    """

    read_ts: dt.datetime
    zone: str
    days: list[dt.date]
    weeks: list[dt.date]
    totals: RepoRollup
    repos: list[BoardRepo]
