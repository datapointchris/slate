import datetime as dt
from typing import Annotated
from typing import Literal
from typing import Self
from uuid import UUID

from pydantic import ConfigDict
from pydantic import StringConstraints
from pydantic import model_validator

from slate.schemas.claim import Claim
from slate.schemas.initiative import Initiative
from slate.schemas.initiative import InitiativeRef
from slate.schemas.initiative import InitiativeWritten
from slate.schemas.initiative import NotPlannedStep
from slate.schemas.issue import IssuePaths
from slate.schemas.issue import IssueRef
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


PromotedKind = Literal['initiative', 'issue']

# Each kind's own fields on a promotion, each named as that kind's create names it.
FIELDS_OF_KIND: dict[PromotedKind, frozenset[str]] = {
    'initiative': frozenset({'summary', 'name', 'status', 'autofix', 'pursuits', 'not_planned_steps'}),
    'issue': frozenset({'repo', 'paths', 'type', 'acceptance', 'initiative', 'parent'}),
}
# The fields a promotion to either kind takes.
SHARED_FIELDS = frozenset({'description', 'priority', 'tags'})


class IdeaPromote(RecordModel):
    """A promotion: `kind`, an initiative by default or an issue, and the fields of that kind.

    An initiative needs `summary`, because an idea has none. A field left out keeps what the record
    holds: the idea's title as the name, its description, its priority, its tags and its pursuits.
    An idea marks no step it will not run, so `not_planned_steps` left out plans every step. An issue
    starts open. The API refuses an unknown field, or one of the other kind, rather than dropping it,
    so a client sending one learns it was not applied.
    """

    model_config = ConfigDict(from_attributes=True, extra='forbid')

    kind: PromotedKind = 'initiative'
    description: str | None = None
    priority: Priority | None = None
    tags: list[str] | None = None
    summary: SummaryText | None = None
    name: Title | None = None
    status: Literal['active', 'someday'] = 'active'
    autofix: bool = False
    pursuits: list[int] | None = None
    not_planned_steps: list[NotPlannedStep] = []
    repo: str | None = None
    paths: IssuePaths = []
    type: str = 'task'
    acceptance: str | None = None
    initiative: InitiativeRef | None = None
    parent: IssueRef | None = None

    @model_validator(mode='after')
    def fits_its_kind(self) -> Self:
        for kind, fields in FIELDS_OF_KIND.items():
            if kind != self.kind and (sent := sorted(self.model_fields_set & fields)):
                raise ValueError(f'{", ".join(sent)} {"applies" if len(sent) == 1 else "apply"} to an {kind}, not an {self.kind}')
        if self.kind == 'initiative' and self.summary is None:
            raise ValueError('summary is required to promote an idea to an initiative, because an idea has none')
        return self


class Idea(InPursuits):
    """An idea, its refs oldest first, and the initiative it became with that initiative's progress.

    `claim` is the idea's unended claim, live or expired. `rank` places it among the records of its
    priority in the queue. `pursuits` is each pursuit it is in, in number order. `flow_id` is the
    flow of the work that filed it, which an initiative promoted from it takes.
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
    initiative: Initiative | None = None
    claim: Claim | None = None


class IdeaDetail(Idea, RecordParts):
    """An idea with its claims, comments and documents."""


class IdeaWritten(Written):
    idea: Idea


class InitiativeWrittenWithIdea(InitiativeWritten):
    """A promotion's answer to an initiative, with `idea` in the shape that CLI releases through `cli/v0.66.0` decode.

    `idea.initiative` names the record itself.
    """

    idea: Idea
