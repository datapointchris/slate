import datetime as dt
from typing import Annotated
from uuid import UUID

from pydantic import AfterValidator
from pydantic import StringConstraints

from slate.schemas.claim import Claim
from slate.schemas.initiative import InitiativeRef
from slate.schemas.initiative import InitiativeSummary
from slate.schemas.not_null import NotNull
from slate.schemas.record import Actor
from slate.schemas.record import Priority
from slate.schemas.record import RecordModel
from slate.schemas.record import RecordSummary
from slate.schemas.record_parts import RecordParts
from slate.schemas.write_effects import Written

# An issue answers to its UUID or its number, and the API resolves either. `str`
# is in the union because a CLI forwards what was typed, so a number arrives as "42".
IssueRef = UUID | int | str

Slug = Annotated[str, StringConstraints(pattern=r'^[a-z0-9]+(-[a-z0-9]+)*$')]

# A path is a prefix, as a git pathspec without wildcards is: a directory covers everything under it.
# Two globs can meet in ways no prefix test sees, so a wildcard is refused rather than read literally.
PATH_WILDCARDS = frozenset('*?[')


def normalized_path(raw: str) -> str:
    """The path as `services/issue_paths.py` compares it: segments joined by `/`, with no `.`, no empty segment and no leading `/`."""
    segments = [segment for segment in raw.strip().split('/') if segment not in ('', '.')]
    if not segments:
        raise ValueError(f'{raw!r} names no file or directory. A path is relative to the repo root, such as cli/internal/cli.')
    if '..' in segments:
        raise ValueError(f'{raw!r} climbs out with "..". A path is relative to the repo root.')
    if PATH_WILDCARDS & set(raw):
        raise ValueError(f'{raw!r} is a glob. A path names a file or a directory, and a directory covers everything under it.')
    return '/'.join(segments)


IssuePath = Annotated[str, AfterValidator(normalized_path)]


def distinct_paths(paths: list[str]) -> list[str]:
    return sorted(set(paths))


IssuePaths = Annotated[list[IssuePath], AfterValidator(distinct_paths)]


class IssueSummary(RecordModel):
    """Enough of another issue to print it beside this one without fetching it."""

    id: UUID
    number: int
    title: str
    status: str


class IssueCreate(RecordModel):
    """`status` takes `triage` for work filed by a machine, which waits there until accepted.

    `paths` are files and directories in `repo` the work changes, so an issue naming any needs a repo.
    """

    title: str
    description: str | None = None
    acceptance: str | None = None
    repo: str | None = None
    paths: IssuePaths = []
    type: str = 'task'
    status: str = 'open'
    priority: Priority = 0
    deferred_until_date: dt.date | None = None
    autofix: bool = False
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
    `status: canceled`, and `labels` and `paths` each replace the issue's whole set.
    """

    title: NotNull[str] = None
    description: str | None = None
    acceptance: str | None = None
    repo: str | None = None
    paths: NotNull[IssuePaths] = None
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
    now, whatever its type; the queue gives a decision its own step.
    `autofix` is the issue's own setting, and `is_autofixable` is false when it,
    its initiative's or an unclosed parent's is false. `claim` is the issue's
    unended claim, live or expired, and null when nobody holds it. `rank` is its
    record's place among every record's. `initiative_waits_on` is each record
    its initiative waits on while that initiative is open, so an issue blocked
    by that wait says by what. `waiting_initiatives` is each initiative waiting
    on this issue.
    """

    id: UUID
    number: int
    title: str
    description: str | None = None
    acceptance: str | None = None
    repo: str | None = None
    paths: list[str] = []
    type: str
    status: str
    status_reason: str | None = None
    priority: int
    effective_priority: int
    rank: float
    pinned_ts: dt.datetime | None = None
    pinned_by: str | None = None
    pinned_session: str | None = None
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
    initiative_waits_on: list[RecordSummary] = []
    waiting_initiatives: list[RecordSummary] = []
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


class IssueDetail(Issue, RecordParts):
    """An issue with its children, claims, comments and documents."""

    children: list[IssueSummary] = []


class IssueDependencyCreate(RecordModel):
    depends_on: IssueRef


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
