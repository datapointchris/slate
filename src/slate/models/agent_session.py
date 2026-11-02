"""An agent session slate has seen, keyed by the id its runtime gave it.

A Claude Code session is keyed by `$CLAUDE_CODE_SESSION_ID`. Any other agent names its own. slate
first sees a session when it holds a claim, starts or joins a worklog, or links a commit, and
records it then. A session's spans in worklogs, and a claim's `session`, reference it here.
"""

import datetime as dt

from sqlalchemy import DateTime
from sqlalchemy import Text
from sqlalchemy import text
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import mapped_column

from slate.database.base import Base


class AgentSession(Base):
    __tablename__ = 'agent_sessions'
    id: Mapped[str] = mapped_column(Text, primary_key=True)
    # The box it runs on, as the client named it the first time slate saw the session.
    host: Mapped[str | None] = mapped_column(Text, nullable=True)
    first_seen_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))

    def __repr__(self) -> str:
        return f'AgentSession(id={self.id!r}, host={self.host!r})'
