import datetime as dt
from typing import Annotated
from uuid import UUID

from pydantic import StringConstraints
from pydantic import computed_field

from slate.schemas.not_null import NotNull
from slate.schemas.record import RecordModel
from slate.schemas.record import RecordSummary
from slate.schemas.record import SummaryText
from slate.schemas.record_parts import RecordParts
from slate.schemas.write_effects import Written

OneLine = Annotated[str, StringConstraints(strip_whitespace=True, min_length=1, pattern=r'^[^\r\n]+$')]


class PursuitSummary(RecordModel):
    """A pursuit as a record in it reads it: the number a write sets, and the name a person knows."""

    number: int
    name: str


class InPursuits(RecordModel):
    """A record read carrying the pursuits it is in, in number order.

    `directions` is the same list under the name the release before pursuits served, so a reader
    from then keeps reading it until a later release drops it.
    """

    pursuits: list[PursuitSummary] = []

    @computed_field  # type: ignore[prop-decorator]
    @property
    def directions(self) -> list[PursuitSummary]:
        return self.pursuits


class PursuitCreate(RecordModel):
    """`summary` says what the condition means, on one line. `measure` names the command that reads where it stands."""

    name: OneLine
    summary: SummaryText
    description: str | None = None
    measure: OneLine | None = None
    status: str = 'active'
    status_reason: str | None = None


class PursuitUpdate(RecordModel):
    """`closed_ts` is stamped on the move into a terminal status and cleared on reopen. A null `measure` clears it."""

    name: NotNull[OneLine] = None
    summary: NotNull[SummaryText] = None
    description: str | None = None
    measure: OneLine | None = None
    status: NotNull[str] = None
    status_reason: str | None = None


class Pursuit(RecordModel):
    """A pursuit and every idea and initiative in it, each by number and in number order, whatever its status.

    `measure` is null where nothing measures the pursuit yet.
    """

    id: UUID
    number: int
    name: str
    summary: str
    description: str | None = None
    measure: str | None = None
    status: str
    status_reason: str | None = None
    created_ts: dt.datetime
    closed_ts: dt.datetime | None = None
    ideas: list[RecordSummary] = []
    initiatives: list[RecordSummary] = []


class PursuitDetail(Pursuit, RecordParts):
    """A pursuit with its claims, comments and documents."""


class PursuitWritten(Written):
    pursuit: Pursuit
