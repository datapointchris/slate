import datetime as dt
from typing import Annotated
from uuid import UUID

from pydantic import StringConstraints
from pydantic import model_validator

from slate.schemas.claim import Claim
from slate.schemas.comment import Comment
from slate.schemas.document import DocumentSummary
from slate.schemas.initiative import InitiativeRef
from slate.schemas.initiative import InitiativeSummary
from slate.schemas.not_null import NotNull
from slate.schemas.record import Actor
from slate.schemas.record import Priority
from slate.schemas.record import RecordModel
from slate.schemas.write_effects import Written

# An issue answers to its UUID or its number, and the API resolves either. `str`
# is in the union because a CLI forwards what was typed, so a number arrives as "42".
IssueRef = UUID | int | str

Slug = Annotated[str, StringConstraints(pattern=r'^[a-z0-9]+(-[a-z0-9]+)*$')]


class IssueSummary(RecordModel):
    """Enough of another issue to print it beside this one without fetching it."""

    id: UUID
    number: int
    title: str
    status: str


class IssueCreate(RecordModel):
    """`status` takes `triage` for work filed by a machine, which waits there until accepted."""

    title: str
    description: str | None = None
    acceptance: str | None = None
    repo: str | None = None
    type: str = 'task'
    status: str = 'open'
    priority: Priority = 0
    deferred_until_date: dt.date | None = None
    autofix: bool = True
    initiative: InitiativeRef | None = None
    parent: IssueRef | None = None
    discovered_from: IssueRef | None = None
    labels: list[Slug] = []
    depends_on: list[IssueRef] = []


class IssueUpdate(RecordModel):
    """Status moves through here, and the server stamps what a transition implies.

    `closed_ts` is never sent: closing stamps it, and ends the issue's claim as
    a side effect the answer names. `closed_by` names who is closing it and is accepted only with the
    status that closes an open issue; the server stamps the signed-in user's
    email when it is left out. `duplicate_of` is accepted only alongside
    `status: canceled`, and `labels` replaces the issue's whole set.
    """

    title: NotNull[str] = None
    description: str | None = None
    acceptance: str | None = None
    repo: str | None = None
    type: NotNull[str] = None
    status: NotNull[str] = None
    status_reason: str | None = None
    priority: NotNull[Priority] = None
    deferred_until_date: dt.date | None = None
    autofix: NotNull[bool] = None
    initiative: InitiativeRef | None = None
    parent: IssueRef | None = None
    discovered_from: IssueRef | None = None
    duplicate_of: IssueRef | None = None
    closed_by: Actor | None = None
    labels: NotNull[list[Slug]] = None


class Issue(RecordModel):
    """An issue travels with everything a list row needs, so no client fans out.

    `effective_priority` is the priority the ready queue sorts by: the most
    urgent of the issue's own, its open parent's, its active initiative's, and
    anything it blocks. `is_ready` says whether an agent could take it
    now, whatever its type; the ready queue leaves decisions out separately.
    `autofix` is the issue's own setting, and `is_autofixable` is false when it,
    its initiative's or an unclosed parent's is false. `claim` is the issue's
    unended claim, live or expired, and null when nobody holds it.
    """

    id: UUID
    number: int
    title: str
    description: str | None = None
    acceptance: str | None = None
    repo: str | None = None
    type: str
    status: str
    status_reason: str | None = None
    priority: int
    effective_priority: int
    rank: float
    deferred_until_date: dt.date | None = None
    claim: Claim | None = None
    autofix: bool
    initiative: InitiativeSummary | None = None
    parent: IssueSummary | None = None
    discovered_from: IssueSummary | None = None
    duplicate_of: IssueSummary | None = None
    labels: list[str] = []
    depends_on: list[IssueSummary] = []
    blocks: list[IssueSummary] = []
    child_count: int = 0
    open_child_count: int = 0
    comment_count: int = 0
    is_blocked: bool
    is_ready: bool
    is_autofixable: bool
    created_ts: dt.datetime
    updated_ts: dt.datetime
    closed_ts: dt.datetime | None = None
    closed_by: str | None = None


class IssueDetail(Issue):
    """`claims` is every claim the issue has had, ended ones included, and `comments` every comment on it, each oldest first."""

    children: list[IssueSummary] = []
    comments: list[Comment] = []
    documents: list[DocumentSummary] = []
    claims: list[Claim] = []


class IssueDependencyCreate(RecordModel):
    depends_on: IssueRef


class IssueRankMove(RecordModel):
    """Place an issue immediately before or after another of the same effective priority."""

    before: IssueRef | None = None
    after: IssueRef | None = None

    @model_validator(mode='after')
    def exactly_one_neighbor(self):
        if (self.before is None) == (self.after is None):
            raise ValueError('give exactly one of before or after')
        return self


class IssueLabelCreate(RecordModel):
    slug: Slug
    group_slug: Slug | None = None
    description: str | None = None


class IssueLabelUpdate(RecordModel):
    """The slug is the key and never changes; renaming is delete plus create."""

    group_slug: Slug | None = None
    description: str | None = None


class IssueLabel(RecordModel):
    slug: str
    group_slug: str | None = None
    description: str | None = None
    open_issue_count: int = 0


class IssueWritten(Written):
    issue: IssueDetail


class IssueLabelWritten(Written):
    label: IssueLabel


class IssueLabelDeleted(Written):
    """`deleted` is the label as it stood, its count the unclosed issues that carried it."""

    deleted: IssueLabel
