import datetime as dt
from uuid import UUID

from slate.schemas.not_null import NotNull
from slate.schemas.record import Priority
from slate.schemas.record import RecordModel
from slate.schemas.write_effects import Written

# An initiative answers to its UUID, its number or its name, and the API resolves each.
InitiativeRef = UUID | int | str


class InitiativeSummary(RecordModel):
    id: UUID
    number: int
    name: str
    status: str
    priority: int
    autofix: bool


class InitiativeCreate(RecordModel):
    name: str
    description: str | None = None
    status: str = 'active'
    status_reason: str | None = None
    priority: Priority = 0
    position: int | None = None
    autofix: bool = True


class InitiativeUpdate(RecordModel):
    """`closed_ts` is stamped on the move into a terminal status and cleared on reopen."""

    name: NotNull[str] = None
    description: str | None = None
    status: NotNull[str] = None
    status_reason: str | None = None
    priority: NotNull[Priority] = None
    position: NotNull[int] = None
    autofix: NotNull[bool] = None


class Initiative(RecordModel):
    """An initiative and the counts that say whether work is left in it.

    The three counts partition `issue_count`. `repos` is derived from every issue
    not canceled, never stored, so it cannot drift from what the work touches.
    """

    id: UUID
    number: int
    name: str
    description: str | None = None
    status: str
    status_reason: str | None = None
    priority: int
    position: int
    autofix: bool
    created_ts: dt.datetime
    closed_ts: dt.datetime | None = None
    issue_count: int = 0
    open_count: int = 0
    completed_count: int = 0
    canceled_count: int = 0
    repos: list[str] = []


class InitiativeWritten(Written):
    initiative: Initiative
