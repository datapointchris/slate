"""The queue `slate next` reads: every kind of open work in the order to take it, and the held work apart."""

import datetime as dt
from typing import Annotated
from typing import Literal
from typing import get_args

from pydantic import BaseModel
from pydantic import Field

from slate.schemas.claim import ClaimWait
from slate.schemas.idea import Idea
from slate.schemas.initiative import Initiative
from slate.schemas.initiative import InitiativeSummary
from slate.schemas.issue import Issue
from slate.schemas.pursuit import PursuitSummary
from slate.schemas.ready_filter import ReadyFilter

# What a row asks of whoever takes it, in the order work moves through them. An idea is discussed and
# a decision issue decided. An initiative and any other issue take the worklog's steps, so every
# `StepName` is one of these.
Step = Literal['discuss', 'shape', 'design', 'preflight', 'decide', 'build', 'retro']
QUEUE_STEPS: list[str] = list(get_args(Step))

# What set a row's place, one hop back. `own` is its own priority and rank, `pin` its own pin, and
# `initiative` the slot of the initiative holding it. `parent` and `waiter` raised it just ahead of
# its open parent or of an issue depending on it, which the row's `raised_by` names.
PlacedBy = Literal['own', 'pin', 'initiative', 'parent', 'waiter']


class QueueFilter(ReadyFilter):
    """`GET /queue`'s filters. A filter `reads_only_issues` in `services/work_queue.py` names leaves the ideas and initiatives out."""

    step: Step | None = Field(None, description=f'Only rows at this step: {", ".join(QUEUE_STEPS)}')
    type: str | None = Field(None, description='An issue type. Leaves out every idea and initiative.')
    for_record: int | None = Field(
        None,
        description='A record number. Reads its own open work and every open issue it transitively waits on, ready or not, in queue order.',
    )


class PathOverlap(BaseModel):
    """Another issue under a live claim whose paths meet this one's: who holds it, and its paths that meet."""

    number: int
    title: str
    holder: str
    expires_ts: dt.datetime
    paths: list[str]


class QueueRowBase(BaseModel):
    """`priority` is the one the row sorts at: an issue's effective priority, else the record's own.

    `holder` is who holds a claimed row, and null in the queue. `held_through` is the initiative
    whose live claim holds it, and null where the row's own claim does or nothing does. A row with
    `held_through` has no claim of its own, so `holder` holds the initiative and cannot extend or
    release the issue. `overlaps` names each issue under a live claim whose paths meet this issue's,
    so the collision shows before anyone takes it.
    `pinned` says a pin put the row ahead of every priority: its own, its initiative's, or that of
    what waits on it. The record's `pinned_ts` is set only where its own pin is. `placed_by` says
    what set the row's place, one hop back. `raised_by` is the issue whose wait put this one ahead
    of its own place, which refuses a rank move beside it.
    """

    number: int
    title: str
    step: Step
    priority: int
    pinned: bool = False
    placed_by: PlacedBy
    raised_by: int | None = None
    holder: str | None = None
    held_through: int | None = None
    overlaps: list[PathOverlap] = []


class RecordQueueRow(QueueRowBase):
    """`unblocks` is filled only under `for_record`: each issue in that record's work waiting on this one directly."""

    unblocks: list[int] = []


class IssueRow(RecordQueueRow):
    kind: Literal['issue'] = 'issue'
    record: Issue


class InitiativeRow(RecordQueueRow):
    kind: Literal['initiative'] = 'initiative'
    record: Initiative


class IdeaRow(RecordQueueRow):
    kind: Literal['idea'] = 'idea'
    record: Idea


QueueRow = Annotated[IssueRow | InitiativeRow | IdeaRow, Field(discriminator='kind')]


class Queue(BaseModel):
    """`queue` is the unheld work, first row next. `claimed` is the held work, by holder, then in queue order."""

    queue: list[QueueRow]
    claimed: list[QueueRow]


class QueueMove(BaseModel):
    """Where one move takes a queued row, as the rank write's own rules decide it.

    `beside` is the record that write names: `before` it to move the row ahead, `after` it to move
    the row behind. `passes` lists each queued row that ends up on the row's other side, nearest
    first. `refused` is why the write would be refused, in its own words, and `beside` is then null.
    """

    beside: int | None = None
    passes: list[int] = []
    refused: str | None = None


class QueueRowMoves(BaseModel):
    """A queued row's move ahead of the row above it, and behind the row below it."""

    ahead: QueueMove
    behind: QueueMove


class QueueBoardRow(QueueRowBase):
    """One row of the queue with only what a board draws of it, and no record body.

    `own_priority` is the record's own, beside the `priority` it sorts at. `initiative` is an
    issue's initiative, as an issue's read names it, and `pursuits` that initiative's for an issue, else the record's own.
    `repos` is an issue's repo, or each repo an initiative's issues name. `open_count` is an
    initiative's unclosed issues. `expires_ts` and `waiting` are those of the claim holding a
    claimed row, which is the initiative's where `held_through` is set. `slot` is the record whose
    slot holds the row's place: its own, its initiative's, or the slot of the issue that raised it,
    however many hops back. `moves` says where a move ahead or behind takes a queued row, and is
    null on a claimed row, which holds no place in the order drawn.
    """

    kind: Literal['issue', 'initiative', 'idea']
    own_priority: int
    slot: int
    moves: QueueRowMoves | None = None
    initiative: InitiativeSummary | None = None
    repos: list[str] = []
    pursuits: list[PursuitSummary] = []
    open_count: int | None = None
    expires_ts: dt.datetime | None = None
    waiting: ClaimWait | None = None


class QueueBoard(BaseModel):
    """Every open row of the queue, slim, in the order `Queue` lists them: `queue` first row next, `claimed` by holder."""

    read_ts: dt.datetime
    queue: list[QueueBoardRow]
    claimed: list[QueueBoardRow]
