"""An agent session's read, with the tokens it spent in each worklog and outside them, and the report that replaces them."""

import datetime as dt
from collections import Counter
from typing import Annotated
from typing import Self

from pydantic import AwareDatetime
from pydantic import Field
from pydantic import model_validator

from slate.schemas.record import RecordModel
from slate.schemas.tokens import TokensByKind
from slate.schemas.worklog import NonBlank
from slate.schemas.write_effects import Written


class MessageTokensReport(RecordModel):
    key: Annotated[
        NonBlank,
        Field(description="The provider's message id, or where the message was read when it carries none. Unique across sessions"),
    ]
    written_ts: Annotated[AwareDatetime, Field(description='When the message was written; it places the message in a span')]
    model: str | None = None
    subagent: Annotated[str | None, Field(description="The subagent whose transcript holds it; null for the session's own")] = None
    tokens: TokensByKind


class SessionUsageReport(RecordModel):
    """Every message the session has spent tokens on so far. It replaces whatever the session reported before."""

    host: str | None = None
    messages: list[MessageTokensReport]

    @model_validator(mode='after')
    def each_message_once(self) -> Self:
        repeated = sorted(key for key, seen in Counter(message.key for message in self.messages).items() if seen > 1)
        if repeated:
            raise ValueError(f'each message is reported once, and these keys repeat: {", ".join(repeated)}')
        return self


class SessionSpan(RecordModel):
    """The session's time in one worklog, and the tokens it spent there: null while no message it reported landed there."""

    worklog: str
    record_number: int
    started_ts: dt.datetime
    ended_ts: dt.datetime | None = None
    tokens: TokensByKind | None = None


class AgentSession(RecordModel):
    """A session slate has seen, with its tokens by kind: in all, in each span, and outside every worklog.

    `message_count` counts the messages slate holds for it. `usage_reported_ts` is when a report last
    changed them, and null until one has. Each sum is null while no message it covers was reported.
    `tokens_outside_worklogs` is a count once the session reported any message, and zero when every
    one fell in a worklog, so a session working only inside worklogs never reads as unreported.
    """

    id: str
    host: str | None = None
    first_seen_ts: dt.datetime
    usage_reported_ts: dt.datetime | None = None
    message_count: int
    tokens: TokensByKind | None = None
    tokens_outside_worklogs: TokensByKind | None = None
    spans: list[SessionSpan]


class SessionWritten(Written):
    session: AgentSession
