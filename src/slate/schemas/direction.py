import datetime as dt
from typing import Annotated
from uuid import UUID

from pydantic import StringConstraints

from slate.schemas.not_null import NotNull
from slate.schemas.record import RecordModel
from slate.schemas.record import RecordSummary
from slate.schemas.record import SummaryText
from slate.schemas.record_parts import RecordParts
from slate.schemas.write_effects import Written

DirectionName = Annotated[str, StringConstraints(strip_whitespace=True, min_length=1, pattern=r'^[^\r\n]+$')]


class DirectionSummary(RecordModel):
    """A direction as a record in it reads it: the number a write sets, and the name a person knows."""

    number: int
    name: str


class DirectionCreate(RecordModel):
    """`summary` says where the direction ends, on one line."""

    name: DirectionName
    summary: SummaryText
    description: str | None = None
    status: str = 'active'
    status_reason: str | None = None


class DirectionUpdate(RecordModel):
    """`closed_ts` is stamped on the move into a terminal status and cleared on reopen."""

    name: NotNull[DirectionName] = None
    summary: NotNull[SummaryText] = None
    description: str | None = None
    status: NotNull[str] = None
    status_reason: str | None = None


class Direction(RecordModel):
    """A direction and every idea and initiative in it, each by number and in number order, whatever its status."""

    id: UUID
    number: int
    name: str
    summary: str
    description: str | None = None
    status: str
    status_reason: str | None = None
    created_ts: dt.datetime
    closed_ts: dt.datetime | None = None
    ideas: list[RecordSummary] = []
    initiatives: list[RecordSummary] = []


class DirectionDetail(Direction, RecordParts):
    """A direction with its claims, comments and documents."""


class DirectionWritten(Written):
    direction: Direction
