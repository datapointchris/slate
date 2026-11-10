"""The totals of the work over a record, a worklog, a repo and a window, each figure derived on read."""

import datetime as dt
from typing import Annotated

from pydantic import Field

from slate.schemas.record import RecordModel
from slate.schemas.record import RecordSummary
from slate.schemas.tokens import TokenEstimate
from slate.schemas.tokens import TokensByKind


class PreflightEstimate(RecordModel):
    """The estimate a record's latest preflight worklog holding one set, and that worklog's name."""

    worklog: str
    tokens: TokenEstimate


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
    """Worklogs, sessions, links, messages and tokens by kind over one scope.

    `docs/totals.md` says what each scope reaches and what each figure counts. `link_counts` holds
    every link kind, zero where the scope linked none. `tokens` is null while no message in the
    scope was reported, so a scope nobody reported for never reads as having spent nothing.
    `tokens_outside_worklogs` is the part of `tokens` no worklog held. Only a scope naming no
    record, worklog or repo reaches that part, so it is null on every other scope.

    `estimate` is what the scope's record expected to spend, so a reader sets `tokens` beside it
    kind by kind. It is null where the scope names no record, or no preflight worklog on the record
    holds one. The worklog, repo and window narrow `tokens` and never the estimate.
    """

    scope: TotalsScope
    worklog_count: int
    session_count: int
    link_counts: Annotated[dict[str, int], Field(description="Each link kind's distinct refs, such as {'commit': 2}")]
    message_count: int
    tokens: TokensByKind | None = None
    tokens_outside_worklogs: TokensByKind | None = None
    tokens_by_model: list[ModelTokens]
    estimate: PreflightEstimate | None = None
