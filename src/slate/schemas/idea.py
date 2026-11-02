import datetime as dt
from typing import Annotated
from typing import Literal
from uuid import UUID

from pydantic import Field
from pydantic import StringConstraints

from slate.schemas.claim import Claim
from slate.schemas.comment import Comment
from slate.schemas.document import DocumentSummary
from slate.schemas.initiative import Initiative
from slate.schemas.not_null import NotNull
from slate.schemas.record import Actor
from slate.schemas.record import Priority
from slate.schemas.record import RecordModel
from slate.schemas.write_effects import Written

# An idea answers to its UUID or its number.
IdeaRef = UUID | int | str

Title = Annotated[str, StringConstraints(strip_whitespace=True, min_length=1)]

# A key the system holding the evidence gave it, such as an inbox row's, or a URL. slate stores it
# as given and never follows it.
EvidenceRef = Annotated[str, StringConstraints(strip_whitespace=True, min_length=1, max_length=2048)]

EvidenceKind = Literal['url', 'key']


class IdeaCreate(RecordModel):
    title: Title
    description: str | None = None
    priority: Priority = 0


class IdeaUpdate(RecordModel):
    title: NotNull[Title] = None
    description: str | None = None
    priority: NotNull[Priority] = None


class IdeaEvidenceLink(RecordModel):
    """Evidence to link, each ref once. A ref the idea already links is left as it is, with a warning."""

    refs: list[EvidenceRef] = Field(min_length=1)
    note: str | None = None
    linked_by: Actor | None = None


class IdeaPromote(RecordModel):
    """The initiative a promotion creates. Each field left out takes the idea's own."""

    name: Title | None = None
    description: str | None = None
    priority: Priority | None = None
    status: Literal['active', 'someday'] = 'active'
    autofix: bool = True


class IdeaEvidence(RecordModel):
    """`kind` is derived from the ref: `url` for an http or https URL, `key` for anything else."""

    ref: str
    kind: EvidenceKind
    note: str | None = None
    linked_by: str | None = None
    linked_ts: dt.datetime


class Idea(RecordModel):
    """An idea, its evidence oldest first, and the initiative it became with that initiative's progress.

    `claim` is the idea's unended claim, live or expired.
    """

    id: UUID
    number: int
    title: str
    description: str | None = None
    priority: int
    created_ts: dt.datetime
    updated_ts: dt.datetime
    evidence: list[IdeaEvidence] = []
    initiative: Initiative | None = None
    claim: Claim | None = None


class IdeaDetail(Idea):
    """An idea with every claim ever held on it and every comment on it, oldest first, and its documents."""

    claims: list[Claim] = []
    comments: list[Comment] = []
    documents: list[DocumentSummary] = []


class IdeaWritten(Written):
    idea: Idea
