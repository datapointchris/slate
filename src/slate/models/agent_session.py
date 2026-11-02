"""An agent session slate has seen, keyed by the id its runtime gave it, and the tokens its messages spent.

A Claude Code session is keyed by `$CLAUDE_CODE_SESSION_ID`. Any other agent names its own. slate
first sees a session when it holds a claim, starts or joins a worklog, links a commit, or reports its
usage, and records it then. A session's spans in worklogs, and a claim's `session`, reference it here.
"""

import datetime as dt

from sqlalchemy import BigInteger
from sqlalchemy import CheckConstraint
from sqlalchemy import DateTime
from sqlalchemy import ForeignKey
from sqlalchemy import Index
from sqlalchemy import Integer
from sqlalchemy import Text
from sqlalchemy import text
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import mapped_column

from slate.database.base import Base
from slate.models.record_change import RECORDED


class AgentSession(Base):
    __tablename__ = 'agent_sessions'
    id: Mapped[str] = mapped_column(Text, primary_key=True)
    # The box it runs on, as the client named it the first time slate saw the session.
    host: Mapped[str | None] = mapped_column(Text, nullable=True)
    first_seen_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))
    # When a report last changed its messages' tokens. Null until one has.
    usage_reported_ts: Mapped[dt.datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)

    def __repr__(self) -> str:
        return f'AgentSession(id={self.id!r}, host={self.host!r})'


class MessageTokens(Base):
    """One model message's tokens by kind, as the session reported it, and the span it was spent in.

    A row mirrors one message in a transcript, so its key is the message, with no surrogate. A
    session continued under a new id repeats its parent's messages, so the session that reports a
    message first holds it. Each report replaces its session's rows: it upserts every message it
    names and deletes the rest. The transcript is their record, and `agent_sessions` audits each
    report that changes them, so these rows opt out of the audit trail rather than copy every
    message into it twice.
    """

    __tablename__ = 'message_tokens'
    # The provider's message id, or where the message was read when it carries none, unique across sessions.
    key: Mapped[str] = mapped_column(Text, primary_key=True)
    session_id: Mapped[str] = mapped_column(Text, ForeignKey('agent_sessions.id', ondelete='CASCADE'), nullable=False)
    # When the message was written, on the agent's clock. Placing it in a span compares it with the API's.
    written_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    model: Mapped[str | None] = mapped_column(Text, nullable=True)
    # The subagent whose transcript holds it. Null for the session's own messages.
    subagent: Mapped[str | None] = mapped_column(Text, nullable=True)
    uncached_input_tokens: Mapped[int] = mapped_column(Integer, nullable=False)
    cache_write_tokens: Mapped[int] = mapped_column(Integer, nullable=False)
    cache_read_tokens: Mapped[int] = mapped_column(Integer, nullable=False)
    output_tokens: Mapped[int] = mapped_column(Integer, nullable=False)
    # The span the session was in at `written_ts`. Null is time outside every worklog, the session's alone.
    span_id: Mapped[int | None] = mapped_column(BigInteger, ForeignKey('worklog_spans.id', ondelete='SET NULL'), nullable=True)

    __table_args__ = (
        CheckConstraint(
            'uncached_input_tokens >= 0 AND cache_write_tokens >= 0 AND cache_read_tokens >= 0 AND output_tokens >= 0',
            name='token_counts_are_not_negative',
        ),
        Index('idx_message_tokens_session_id', 'session_id'),
        Index('idx_message_tokens_span_id', 'span_id'),
        Index('idx_message_tokens_written_ts', 'written_ts'),
        {'info': {RECORDED: False}},
    )
