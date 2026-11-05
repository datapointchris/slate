import datetime as dt
from typing import Annotated
from typing import Any
from uuid import UUID

from pydantic import StringConstraints
from pydantic import model_validator

from slate.schemas.claim import Claim
from slate.schemas.not_null import NotNull
from slate.schemas.record import Priority
from slate.schemas.record import RecordModel
from slate.schemas.record import RecordSummary
from slate.schemas.record_parts import RecordParts
from slate.schemas.write_effects import Written

# An initiative answers to its UUID, its number or its name, and the API resolves each.
InitiativeRef = UUID | int | str

SUMMARY_CHARS = 240

# What is true when the work is done, on one line a board draws under the name.
SummaryText = Annotated[str, StringConstraints(strip_whitespace=True, min_length=1, max_length=SUMMARY_CHARS, pattern=r'^[^\r\n]+$')]


class InitiativeSummary(RecordModel):
    id: UUID
    number: int
    name: str
    status: str
    priority: int
    autofix: bool


class InitiativeWrite(RecordModel):
    """A create or an edit. `position` is refused by name, so a client sending it learns what places an initiative now."""

    @model_validator(mode='before')
    @classmethod
    def refuse_position(cls, data: Any) -> Any:
        if isinstance(data, dict) and 'position' in data:
            raise ValueError(
                'position is not a field: an initiative is placed in the queue by POST /initiatives/{id}/rank, '
                'which `slate initiatives reorder` sends'
            )
        return data


class InitiativeCreate(InitiativeWrite):
    name: str
    summary: SummaryText
    description: str | None = None
    status: str = 'active'
    status_reason: str | None = None
    priority: Priority = 0
    autofix: bool = True


class InitiativeUpdate(InitiativeWrite):
    """`closed_ts` is stamped on the move into a terminal status and cleared on reopen."""

    name: NotNull[str] = None
    summary: NotNull[SummaryText] = None
    description: str | None = None
    status: NotNull[str] = None
    status_reason: str | None = None
    priority: NotNull[Priority] = None
    autofix: NotNull[bool] = None


class Initiative(RecordModel):
    """An initiative and the counts that say whether work is left in it.

    The three counts partition `issue_count`. `repos` is derived from every issue
    not canceled, never stored, so it cannot drift from what the work touches.
    `claim` is the initiative's unended claim, live or expired. `idea` is the
    idea promoted into it. `rank` places it, with its issues, among the
    records of its priority in the queue.
    """

    id: UUID
    number: int
    name: str
    summary: str
    description: str | None = None
    status: str
    status_reason: str | None = None
    priority: int
    rank: float
    pinned_ts: dt.datetime | None = None
    pinned_by: str | None = None
    pinned_session: str | None = None
    autofix: bool
    created_ts: dt.datetime
    closed_ts: dt.datetime | None = None
    issue_count: int = 0
    open_count: int = 0
    completed_count: int = 0
    canceled_count: int = 0
    repos: list[str] = []
    claim: Claim | None = None
    idea: RecordSummary | None = None


class InitiativeDetail(Initiative, RecordParts):
    """An initiative with its claims, comments and documents."""


class InitiativeWritten(Written):
    initiative: Initiative
