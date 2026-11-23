import datetime as dt
from typing import Annotated
from typing import Literal
from uuid import UUID

from pydantic import ConfigDict
from pydantic import StringConstraints

from slate.schemas.claim import Claim
from slate.schemas.initiative import Initiative
from slate.schemas.initiative import NotPlannedStep
from slate.schemas.not_null import NotNull
from slate.schemas.pursuit import InPursuits
from slate.schemas.record import Priority
from slate.schemas.record import RecordModel
from slate.schemas.record import SummaryText
from slate.schemas.record_parts import RecordParts
from slate.schemas.record_ref import RecordRef
from slate.schemas.record_ref import RefText
from slate.schemas.write_effects import Written

# An idea answers to its UUID or its number.
IdeaRef = UUID | int | str

Title = Annotated[str, StringConstraints(strip_whitespace=True, min_length=1)]


class IdeaCreate(RecordModel):
    """`refs` are linked as the caller, each once."""

    title: Title
    description: str | None = None
    priority: Priority = 0
    tags: list[str] = []
    pursuits: list[int] = []
    refs: list[RefText] = []


class IdeaUpdate(RecordModel):
    """`tags` and `pursuits` each replace the idea's whole set."""

    title: NotNull[Title] = None
    description: str | None = None
    priority: NotNull[Priority] = None
    tags: NotNull[list[str]] = None
    pursuits: NotNull[list[int]] = None


class IdeaPromote(RecordModel):
    """The initiative a promotion creates. An idea has no summary, so one is required. Each other field left out takes the idea's own.

    An idea marks no step it will not run, so `not_planned_steps` left out plans every step. A field
    it does not name is refused rather than dropped, so a client sending one learns it was not applied.
    """

    model_config = ConfigDict(from_attributes=True, extra='forbid')

    summary: SummaryText
    name: Title | None = None
    description: str | None = None
    priority: Priority | None = None
    status: Literal['active', 'someday'] = 'active'
    autofix: bool = False
    tags: list[str] | None = None
    pursuits: list[int] | None = None
    not_planned_steps: list[NotPlannedStep] = []


class Idea(InPursuits):
    """An idea, its refs oldest first, and the initiative it became with that initiative's progress.

    `claim` is the idea's unended claim, live or expired. `rank` places it among the records of its
    priority in the queue. `pursuits` is each pursuit it is in, in number order. `flow_id` is the
    flow of the work that filed it, which an initiative promoted from it takes. `evidence` is
    `refs` again, under the name the release before refs served.
    """

    id: UUID
    number: int
    title: str
    description: str | None = None
    priority: int
    rank: float
    pinned_ts: dt.datetime | None = None
    pinned_by: str | None = None
    pinned_session: str | None = None
    flow_id: str | None = None
    created_ts: dt.datetime
    updated_ts: dt.datetime
    tags: list[str] = []
    refs: list[RecordRef] = []
    evidence: list[RecordRef] = []
    initiative: Initiative | None = None
    claim: Claim | None = None


class IdeaDetail(Idea, RecordParts):
    """An idea with its claims, comments and documents."""


class IdeaWritten(Written):
    idea: Idea
