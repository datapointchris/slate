"""The queue `slate next` reads: every kind of open work in the order to take it, and the held work apart."""

import datetime as dt
from typing import Annotated
from typing import Literal

from pydantic import BaseModel
from pydantic import Field

from slate.schemas.idea import Idea
from slate.schemas.initiative import Initiative
from slate.schemas.issue import Issue
from slate.schemas.ready_filter import ReadyFilter

# What a row asks of whoever takes it: discuss an idea, plan an initiative with no issue left in it,
# decide a decision issue, build any other issue. A held initiative whose issues are open is at `build`.
Step = Literal['discuss', 'plan', 'decide', 'build']


class QueueFilter(ReadyFilter):
    """`GET /queue`'s filters. Every one but `step` and `initiative` reads only issues, so it leaves the other kinds out."""

    step: Step | None = Field(None, description='Only rows at this step: discuss, plan, decide or build')
    type: str | None = Field(None, description='An issue type. Leaves out every idea and initiative.')


class PathOverlap(BaseModel):
    """Another issue under a live claim whose paths meet this one's: who holds it, and its paths that meet."""

    number: int
    title: str
    holder: str
    expires_ts: dt.datetime
    paths: list[str]


class QueueRowBase(BaseModel):
    """`priority` is the one the row sorts at: an issue's effective priority, else the record's own.

    `holder` is who holds a claimed row, and null in the queue. `overlaps` names each issue under
    a live claim whose paths meet this issue's, so the collision shows before anyone takes it.
    `pinned` says a pin put the row ahead of every priority: its own, its initiative's, or that of
    what waits on it. The record's `pinned_ts` is set only where its own pin is. `raised_by` is
    the issue whose wait put this one ahead of its own place, which refuses a rank move beside it.
    """

    number: int
    title: str
    step: Step
    priority: int
    pinned: bool = False
    raised_by: int | None = None
    holder: str | None = None
    overlaps: list[PathOverlap] = []


class IssueRow(QueueRowBase):
    kind: Literal['issue'] = 'issue'
    record: Issue


class InitiativeRow(QueueRowBase):
    kind: Literal['initiative'] = 'initiative'
    record: Initiative


class IdeaRow(QueueRowBase):
    kind: Literal['idea'] = 'idea'
    record: Idea


QueueRow = Annotated[IssueRow | InitiativeRow | IdeaRow, Field(discriminator='kind')]


class Queue(BaseModel):
    """`queue` is the unheld work, first row next. `claimed` is the held work, by holder, then in queue order."""

    queue: list[QueueRow]
    claimed: list[QueueRow]
