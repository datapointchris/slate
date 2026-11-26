import datetime as dt
from typing import Annotated
from typing import Literal

from pydantic import ConfigDict
from pydantic import Field
from pydantic import StringConstraints
from pydantic import model_validator

from slate.models.worklog import StepName
from slate.schemas.ready_filter import ReadyFilter
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
    """Who takes a claim, for how long, where they work while they hold it, and the step its worklog performs."""

    holder: Actor
    minutes: ClaimMinutes = DEFAULT_CLAIM_MINUTES
    session: str | None = None
    host: str | None = None
    directory: str | None = None
    step: StepName | None = None


class ClaimHold(ClaimLease):
    record: RecordRef


class ClaimHoldNext(ClaimLease, ReadyFilter):
    """Hold the head of the queue's `build` step, narrowed by the filters the queue reads."""

    @model_validator(mode='after')
    def refuse_decisions(self):
        if self.type == 'decision':
            raise ValueError(
                'a decision waits on a person, so the next hold is taken only from build work. '
                'GET /queue?step=decide lists the decisions, and a hold naming one takes it'
            )
        return self


class ClaimExtend(RecordModel):
    holder: Actor
    minutes: ClaimMinutes = DEFAULT_CLAIM_MINUTES


class ClaimRelease(RecordModel):
    released_by: Actor


class ClaimWaitStart(RecordModel):
    holder: Actor
    waiting_on: Annotated[str, StringConstraints(strip_whitespace=True, min_length=1), Field(description='What the holder waits on')]


class ClaimResume(RecordModel):
    """End the wait. A `session` named here goes back into the claim's worklog."""

    holder: Actor
    session: str | None = None
    host: str | None = None
    directory: str | None = None


class ClaimAdvance(RecordModel):
    """Move the holder's claim to a worklog of `step`, ending the one it is on. A field it does not name is refused.

    `session` moves into the new worklog. `minutes` is how long the claim holds again where it had run out.
    """

    model_config = ConfigDict(from_attributes=True, extra='forbid')

    holder: Actor
    step: StepName
    minutes: ClaimMinutes = DEFAULT_CLAIM_MINUTES
    session: str | None = None
    host: str | None = None
    directory: str | None = None


class ClaimAdopt(RecordModel):
    """Take over the claim that `from_session` holds, at the step its worklog performs. A field it does not name is refused."""

    model_config = ConfigDict(from_attributes=True, extra='forbid')

    holder: Actor
    from_session: Annotated[
        str, StringConstraints(strip_whitespace=True, min_length=1), Field(description='The session whose claim it takes')
    ]
    minutes: ClaimMinutes = DEFAULT_CLAIM_MINUTES
    session: str | None = None
    host: str | None = None
    directory: str | None = None


class ClaimWait(RecordModel):
    """A stretch the holder spent waiting while the claim still held its record."""

    waiting_on: str
    started_ts: dt.datetime
    ended_ts: dt.datetime | None = None
    ended_by: str | None = None


class Claim(RecordModel):
    """One lease on a record. `ending` and `ended_ts` are set together, once it ends.

    `state` is derived on every read from `expires_ts` and `ended_ts`. `worklog` names the worklog
    the hold opened, or the one its session started or joined on the record since. `waiting` is what
    the holder waits on now, or null while it works.
    `flow_id` is the flow of the request that took it, not one copied from its record.
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
    flow_id: str | None = None
    state: ClaimState
    worklog: str | None = None
    waiting: ClaimWait | None = None


class ClaimOnRecord(Claim):
    record: RecordSummary


class ClaimWritten(Written):
    """`claim` is null when a release found nothing held, or a hold of the next issue found nothing ready."""

    claim: ClaimOnRecord | None = None
