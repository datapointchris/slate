"""A worklog's read, and the requests that start, join, finish, link and edit one."""

import datetime as dt
from typing import Annotated
from typing import Literal

from pydantic import Field
from pydantic import StringConstraints

from slate.models.worklog import LinkKindName
from slate.models.worklog import StepName
from slate.models.worklog import WorklogStage
from slate.schemas.claim import Claim
from slate.schemas.claim import ClaimWait
from slate.schemas.document import DocumentVersionRef
from slate.schemas.record import Actor
from slate.schemas.record import RecordModel
from slate.schemas.record import RecordRef
from slate.schemas.record import RecordSummary
from slate.schemas.tokens import TokensByKind
from slate.schemas.write_effects import Written

# The same three a claim reads as. `live` is under way. `expired` is under a claim that ran out,
# which no write has ended yet. `ended` is history.
WorklogState = Literal['live', 'expired', 'ended']

NonBlank = Annotated[str, StringConstraints(strip_whitespace=True, min_length=1)]

# The furthest step checked on a record, or `idea` before any.
RecordStage = Literal['idea'] | StepName


class WorklogStep(RecordModel):
    """One step of a record's life, at its `position` in the order a record goes through them."""

    name: str
    description: str
    position: int


class CheckedStep(RecordModel):
    """A step checked on a record, read from the latest worklog of that step that ended by any outcome but `expired`.

    `session` is the first agent session that worked in that worklog, null where none did.
    """

    step: StepName
    worklog: str
    ended_ts: dt.datetime
    session: str | None = None


class WorklogSpan(RecordModel):
    """One session's time in the worklog, and the tokens it spent there.

    `ended_ts` is null while the session is still in it. `tokens` is null while no message the
    session reported landed in it.
    """

    session: str
    directory: str | None = None
    started_ts: dt.datetime
    ended_ts: dt.datetime | None = None
    tokens: TokensByKind | None = None


class WorklogLink(RecordModel):
    kind: str
    ref: str
    repo: str | None = None
    session: str | None = None
    linked_ts: dt.datetime
    linked_by: str


class Worklog(RecordModel):
    """One go at one record, named `<record>.<sequence>`.

    `state`, `stage` and `tokens` are derived on every read. `claim` is the claim whose hold opened
    it, and `waits` every stretch that claim's holder spent waiting. `tokens` sums its spans', as
    each session last reported them, and is null while none of its spans holds a reported message.
    `document_versions` is every version a session wrote while in it, oldest first. `step` is the
    step of its record's life it performed, null where it names none.
    """

    name: str
    record_number: int
    sequence: int
    record: RecordSummary
    opened_by: str
    step: str | None = None
    started_ts: dt.datetime
    ended_ts: dt.datetime | None = None
    outcome: str | None = None
    ended_by: str | None = None
    state: WorklogState
    stage: WorklogStage
    claim: Claim | None = None
    waits: list[ClaimWait] = []
    tokens: TokensByKind | None = None
    spans: list[WorklogSpan] = []
    links: list[WorklogLink] = []
    document_versions: list[DocumentVersionRef] = []


class WorklogStart(RecordModel):
    """Open a worklog with no claim. `session` joins it, and leaves any other worklog it was in."""

    record: RecordRef
    opened_by: Actor
    step: StepName | None = None
    session: NonBlank | None = None
    host: str | None = None
    directory: str | None = None


class WorklogJoin(RecordModel):
    session: NonBlank
    host: str | None = None
    directory: str | None = None


class WorklogFinish(RecordModel):
    finished_by: Actor


class WorklogLinkAdd(RecordModel):
    kind: LinkKindName
    ref: Annotated[NonBlank, Field(description="A commit's sha, or a pull request's URL")]
    repo: str | None = None
    session: NonBlank | None = None
    linked_by: Actor


class WorklogUpdate(RecordModel):
    """`step: null` clears it."""

    step: StepName | None = None


class WorklogWritten(Written):
    worklog: Worklog
