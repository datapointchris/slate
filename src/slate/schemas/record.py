import datetime as dt
from typing import Annotated
from uuid import UUID

from pydantic import BaseModel
from pydantic import ConfigDict
from pydantic import Field
from pydantic import StringConstraints
from pydantic import model_validator

# A record named by its number, as `42` or `#42`. A CLI forwards what was typed, so a number
# arrives as a string as often as an int.
RecordRef = int | str

Priority = Annotated[int, Field(ge=0, le=4, description='0 none, 1 urgent, 2 high, 3 medium, 4 low')]

# Who acted, as the client names them: `claude-code/<session>`, `worker/<name>`, `user@host`.
Actor = Annotated[str, StringConstraints(strip_whitespace=True, min_length=1)]

SUMMARY_CHARS = 240

# Where an initiative or a direction ends, on one line a board draws under the name.
SummaryText = Annotated[str, StringConstraints(strip_whitespace=True, min_length=1, max_length=SUMMARY_CHARS, pattern=r'^[^\r\n]+$')]


class RecordModel(BaseModel):
    """A schema read from a model row's attributes, which every schema of a record is."""

    model_config = ConfigDict(from_attributes=True)


class RecordDependencyCreate(BaseModel):
    """The record waited on: an issue by number or UUID, or, for an initiative, an initiative or an issue, an initiative also by name."""

    depends_on: UUID | int | str


class RecordSummary(RecordModel):
    """Enough of a record of any kind to print it on one line. `title` is an initiative's name."""

    number: int
    kind: str
    title: str
    status: str | None = None


class RecordListed(RecordSummary):
    """A record of any kind in a list across kinds, with the priority every kind carries."""

    priority: int
    created_ts: dt.datetime


class RecordPin(RecordModel):
    """Pin a record ahead of every priority. `session` is the agent session pinning it, where one is."""

    session: Annotated[str, StringConstraints(strip_whitespace=True, min_length=1)] | None = None
    host: str | None = None


class RecordRankMove(RecordModel):
    """Place a record directly before or after another, of any kind, in the queue `slate next` reads."""

    before: RecordRef | None = None
    after: RecordRef | None = None

    @model_validator(mode='after')
    def exactly_one_neighbor(self):
        if (self.before is None) == (self.after is None):
            raise ValueError('give exactly one of before or after')
        return self
