"""Every initiative on one board, in a read cheap enough to poll, and its long text in a second read polled less often."""

import datetime as dt

from slate.schemas.claim import Claim
from slate.schemas.direction import DirectionSummary
from slate.schemas.initiative_progress import IssueState
from slate.schemas.initiative_progress import IssueStateCounts
from slate.schemas.record import Priority
from slate.schemas.record import RecordModel
from slate.schemas.worklog import CheckedStep
from slate.schemas.worklog import RecordStage


class BoardCloses(RecordModel):
    """The issues completed inside each window, ending at the read.

    A canceled issue never counts as a close, and a completed one whose close has no recorded time
    counts in no window. `by_day` holds one count per day of `InitiativeBoard.days`, in that order.
    """

    last_24h: int
    last_7d: int
    last_30d: int
    last_closed_ts: dt.datetime | None
    by_day: list[int]


class BoardCounts(RecordModel):
    """A row's issues. `states` partitions `issue_count`, and `open_count` is those neither completed nor canceled."""

    issue_count: int
    open_count: int
    states: IssueStateCounts
    closes: BoardCloses


class BoardRollup(BoardCounts):
    """The counts and closes over a set of initiatives."""

    initiative_count: int


class BoardTier(BoardRollup):
    """The rollup of the initiatives at one priority, by their own priority."""

    priority: Priority


class BoardRepoCounts(BoardCounts):
    """An initiative's issues in one repo, or on none where `repo` is null."""

    repo: str | None


class BoardRepoRollup(BoardRollup):
    """Every initiative's issues in one repo, or on none where `repo` is null. `initiative_count` is the initiatives with a cut there."""

    repo: str | None


class BoardUntouched(RecordModel):
    """The active initiative in a group touched longest ago, and when that was."""

    number: int
    name: str
    touched_ts: dt.datetime


class BoardGroupingRollup(BoardRollup):
    """The board's initiatives in one direction, or carrying one tag, with the group's closes over every initiative in it.

    `initiative_count` and the issue counts cover the initiatives the board holds in the group.
    `closes` adds every issue completed inside its 30-day window in any other initiative in the
    group, whatever that initiative's status, so one completed inside the window still counts.
    `untouched_longest` is the active initiative among those on the board touched longest ago,
    null where none is active.
    """

    untouched_longest: BoardUntouched | None


class BoardDirectionRollup(BoardGroupingRollup):
    """The initiatives in one direction, or in none where `direction` is null."""

    direction: DirectionSummary | None


class BoardTagRollup(BoardGroupingRollup):
    """The initiatives carrying one tag of their own, or none where `tag` is null."""

    tag: str | None


class BoardIssue(RecordModel):
    """One issue on the board. `priority` is its own, `effective_priority` the one it sorts at, and `holder` who holds its live claim."""

    number: int
    title: str
    state: IssueState
    type: str
    priority: Priority
    effective_priority: Priority
    repo: str | None
    holder: str | None
    closed_ts: dt.datetime | None


class BoardInitiative(BoardCounts):
    """One initiative on the board. `issues` holds every issue in it: the unclosed in queue order, then the closed, latest first.

    `by_repo` cuts those issues by the repo each names, most open first and no repo last. An
    initiative with no issues has one empty cut on no repo, so every initiative sits under some repo.
    `steps`, `stage`, `tags` and `directions` are the initiative read's. `touched_ts` is the latest
    of when it was created, when a claim on it or one of its issues started, and when one of its
    issues closed.
    """

    number: int
    name: str
    status: str
    priority: Priority
    tags: list[str]
    directions: list[DirectionSummary]
    repos: list[str]
    pinned_ts: dt.datetime | None
    pinned_by: str | None
    claim: Claim | None
    created_ts: dt.datetime
    closed_ts: dt.datetime | None
    touched_ts: dt.datetime
    issues: list[BoardIssue]
    by_repo: list[BoardRepoCounts]
    steps: list[CheckedStep]
    stage: RecordStage


class InitiativeBoard(RecordModel):
    """The board as of `read_ts`. `days` are the calendar days each `by_day` counts, oldest first, in `zone`.

    `tiers` holds every priority, most urgent first and `none` last, whether or not any initiative
    is at it. `repos` rolls up each repo the initiatives' cuts name, most open first and no repo
    last. `directions` holds every active direction and each other one an initiative it counts is
    in, and `tags` every tag. Each puts the fewest closes in 30 days first, then the rest by name,
    and the initiatives in none last, where any are. `initiatives` comes in the order
    `GET /initiatives` answers.
    """

    read_ts: dt.datetime
    zone: str
    days: list[dt.date]
    totals: BoardRollup
    tiers: list[BoardTier]
    repos: list[BoardRepoRollup]
    directions: list[BoardDirectionRollup]
    tags: list[BoardTagRollup]
    initiatives: list[BoardInitiative]


class BoardIssueText(RecordModel):
    """An issue's first paragraph of prose with its markdown stripped, cut near 300 characters. Null where it has none."""

    number: int
    excerpt: str | None


class BoardInitiativeText(RecordModel):
    """An initiative's long text: its summary, its whole description, and why it was set aside where a reason was given."""

    number: int
    summary: str
    description: str | None
    status_reason: str | None
    issues: list[BoardIssueText]


class InitiativeBoardText(RecordModel):
    """The long text of the initiatives `GET /initiatives/board` lists, in its order, each one's issues by number."""

    read_ts: dt.datetime
    initiatives: list[BoardInitiativeText]
