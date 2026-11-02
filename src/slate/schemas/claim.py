import datetime as dt
from typing import Annotated
from typing import Literal
from uuid import UUID

from pydantic import Field

from slate.schemas.record import Actor
from slate.schemas.record import RecordModel
from slate.schemas.record import RecordRef
from slate.schemas.record import RecordSummary
from slate.schemas.write_effects import Written

ClaimMinutes = Annotated[int, Field(ge=1, le=10080, description='How long the claim holds before it expires')]

DEFAULT_CLAIM_MINUTES = 240

# `live` holds the record. `expired` passed its expiry unended, which frees the record. `ended` is history.
ClaimState = Literal['live', 'expired', 'ended']
CLAIM_STATES: tuple[ClaimState, ...] = ('live', 'expired', 'ended')


class ClaimLease(RecordModel):
    """Who takes a claim, for how long, and where they work while they hold it."""

    holder: Actor
    minutes: ClaimMinutes = DEFAULT_CLAIM_MINUTES
    session: str | None = None
    host: str | None = None
    directory: str | None = None


class ClaimHold(ClaimLease):
    record: RecordRef


class ClaimHoldNext(ClaimLease):
    """Hold the head of the issue ready queue, narrowed by the filters the queue reads."""

    repo: str | None = None
    type: str | None = None
    label: str | None = None
    # Any reference an initiative answers to: its UUID, number or name.
    initiative: UUID | int | str | None = None
    autofixable: bool | None = None


class ClaimExtend(RecordModel):
    holder: Actor
    minutes: ClaimMinutes = DEFAULT_CLAIM_MINUTES


class ClaimRelease(RecordModel):
    released_by: Actor


class Claim(RecordModel):
    """One lease on a record. `ending` and `ended_ts` are set together, once it ends.

    `state` is derived on every read from `expires_ts` and `ended_ts`.
    """

    id: int
    record_number: int
    holder: str
    session: str | None = None
    host: str | None = None
    directory: str | None = None
    started_ts: dt.datetime
    expires_ts: dt.datetime
    ended_ts: dt.datetime | None = None
    ending: str | None = None
    ended_by: str | None = None
    state: ClaimState


class ClaimOnRecord(Claim):
    record: RecordSummary


class ClaimWritten(Written):
    """`claim` is null when a release found nothing held, or a hold of the next issue found nothing ready."""

    claim: ClaimOnRecord | None = None
