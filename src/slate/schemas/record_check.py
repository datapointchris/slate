"""A check's read, the request adding one, the check-next mark, and a row of the due order."""

import datetime as dt
from typing import Annotated
from typing import Literal
from typing import get_args

from pydantic import BaseModel
from pydantic import StringConstraints

from slate.schemas.record import RecordModel
from slate.schemas.record import RecordSummary
from slate.schemas.write_effects import Written

NonBlank = Annotated[str, StringConstraints(strip_whitespace=True, min_length=1)]


class CheckCite(RecordModel):
    """One source a check's evidence rests on: its kind, such as `issue`, `path` or `url`, and the reference within that kind."""

    kind: NonBlank
    ref: NonBlank


class RecordCheckCreate(RecordModel):
    """A check on an issue or an initiative. `verdict` is one of `GET /vocabulary`'s `check_verdicts`.

    `run` names the scheduled run that made it, and `session` the agent session, where one did.
    """

    verdict: NonBlank
    evidence: NonBlank
    cites: list[CheckCite] = []
    run: NonBlank | None = None
    session: NonBlank | None = None
    host: str | None = None


class RecordCheck(RecordModel):
    """One check, as it was written. `checked_by` is a service's client id, or the actor of the person who made it."""

    id: int
    record_number: int
    checked_ts: dt.datetime
    checked_by: str
    verdict: str
    evidence: str
    cites: list[CheckCite]
    run: str | None = None
    session: str | None = None


class LastCheck(RecordModel):
    """A record's latest check, as every read of it carries it."""

    verdict: str
    checked_ts: dt.datetime
    checked_by: str


class CheckRequest(RecordModel):
    """Someone asking that a record be checked ahead of its interval. The next check on the record clears it."""

    requested_ts: dt.datetime
    requested_by: str
    reason: str


class CheckRequestCreate(RecordModel):
    reason: NonBlank


class RecordCheckWritten(Written):
    check: RecordCheck


class CheckRequestWritten(Written):
    """`check_request` is the record's mark after the write, and null once it is withdrawn."""

    number: int
    check_request: CheckRequest | None


# Why a record is due, one value per rung of the due order, in the order the rungs run.
DueBecause = Literal['incoming', 'requested', 'never_checked', 'queue_head', 'stale']
DUE_BECAUSE: list[str] = list(get_args(DueBecause))


class DueCheck(BaseModel):
    """One record due a check, and why. `repo` is an issue's, and null on an initiative.

    `likely_duplicates` is each open record whose title reads like this one's, most alike first, so
    whoever checks it starts from the candidates.
    """

    number: int
    kind: Literal['issue', 'initiative']
    title: str
    status: str
    priority: int
    repo: str | None = None
    due_because: DueBecause
    last_check: LastCheck | None = None
    check_request: CheckRequest | None = None
    likely_duplicates: list[RecordSummary] = []
