"""The totals of the work over a record, a worklog, a repo and a window, each figure derived on read."""

import datetime as dt

from slate.schemas.record import RecordModel
from slate.schemas.record import RecordSummary
from slate.schemas.tokens import TokensByKind


class TotalsScope(RecordModel):
    """What the totals cover, as the read named it. Each field left null narrows nothing."""

    record: RecordSummary | None = None
    worklog: str | None = None
    repo: str | None = None
    since: dt.datetime | None = None
    until: dt.datetime | None = None


class ModelTokens(RecordModel):
    """One model's messages and their tokens by kind, so a reader can price each kind at that model's rate."""

    model: str | None = None
    message_count: int
    tokens: TokensByKind


class WorkTotals(RecordModel):
    """Sessions, worklogs, links, messages and tokens by kind over one scope.

    `docs/totals.md` says what each scope reaches and what each figure counts. A sum over no
    message is null, so a scope nobody reported for never reads as having spent nothing.
    `tokens_outside_worklogs` is the part of `tokens` no worklog held, which only a scope naming no
    record, worklog or repo reaches.
    """

    scope: TotalsScope
    worklogs: int
    sessions: int
    commits: int
    pull_requests: int
    landings: int
    message_count: int
    tokens: TokensByKind | None = None
    tokens_outside_worklogs: TokensByKind | None = None
    tokens_by_model: list[ModelTokens]
