import datetime as dt
from typing import Annotated
from uuid import UUID

from pydantic import Field
from pydantic import StringConstraints
from pydantic import model_validator

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
Claimant = Annotated[str, StringConstraints(strip_whitespace=True, min_length=1)]
ClaimMinutes = Annotated[int, Field(ge=1, le=10080, description='How long the claim holds before the issue is ready again')]

DEFAULT_CLAIM_MINUTES = 240


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

    `closed_ts` and the claim are never sent: closing stamps the one and clears
    the other. `closed_by` names who is closing it and is accepted only with the
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
    its initiative's or an unclosed parent's is false.
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
    claimed_by: str | None = None
    claim_expires_ts: dt.datetime | None = None
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


class IssueCommentCreate(RecordModel):
    body: Annotated[str, StringConstraints(strip_whitespace=True, min_length=1)]
    author: str | None = None


class IssueComment(RecordModel):
    id: UUID
    issue_id: UUID
    body: str
    author: str | None = None
    created_ts: dt.datetime


class IssueDetail(Issue):
    children: list[IssueSummary] = []
    comments: list[IssueComment] = []


class IssueDependencyCreate(RecordModel):
    depends_on: IssueRef


class IssueClaimRequest(RecordModel):
    claimant: Claimant
    minutes: ClaimMinutes = DEFAULT_CLAIM_MINUTES


class IssueReadyClaimRequest(IssueClaimRequest):
    """Take the head of the ready queue, narrowed by the same filters the queue reads."""

    repo: str | None = None
    type: str | None = None
    label: Slug | None = None
    initiative: InitiativeRef | None = None
    autofixable: bool | None = None


class IssueClaimResult(RecordModel):
    """`issue` is null when nothing was ready to claim, which is not an error."""

    issue: Issue | None = None


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


class IssuePriorityName(RecordModel):
    value: int
    name: str


class IssueVocabulary(RecordModel):
    """Every value a closed issue field accepts, for a client building its pickers."""

    statuses: list[str]
    types: list[str]
    priorities: list[IssuePriorityName]
    initiative_statuses: list[str]
    labels: list[IssueLabel]


class IssueWritten(Written):
    issue: IssueDetail


class IssueCommentWritten(Written):
    comment: IssueComment


class IssueLabelWritten(Written):
    label: IssueLabel
