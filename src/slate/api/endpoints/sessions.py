"""Agent sessions: each one's read, and the report that replaces the tokens its messages spent.

`slate sessions report` reads a Claude Code transcript and sends its messages here. Any other agent
sends its own the same way, since the write takes counts and never a transcript.
"""

import datetime as dt
from typing import Annotated

import structlog
from fastapi import APIRouter
from fastapi import Depends
from fastapi import status

from slate import models
from slate import schemas
from slate.api.callers import DbSession
from slate.api.exceptions import NotFoundException
from slate.services.session_usage import replace_usage
from slate.services.session_usage import session_view
from slate.services.write_effects import Effects
from slate.tracing.work_on_span import name_agent_session

logger = structlog.get_logger()
router = APIRouter()


def path_session(id: str, session: DbSession) -> models.AgentSession:
    name_agent_session(id)
    agent = session.get(models.AgentSession, id)
    if agent is None:
        raise NotFoundException('session', id, logger)
    return agent


SessionFromPath = Annotated[models.AgentSession, Depends(path_session)]


@router.get('/{id}', response_model=schemas.AgentSession, status_code=status.HTTP_200_OK)
def read_one(agent: SessionFromPath, session: DbSession):
    """One agent session, with its tokens by kind in all, in each worklog span, and outside every worklog."""
    return session_view(session, agent, dt.datetime.now(dt.UTC))


@router.put('/{id}/usage', response_model=schemas.SessionWritten, status_code=status.HTTP_200_OK)
def report_usage(id: str, request: schemas.SessionUsageReport, session: DbSession, effects: Effects):
    """Replace the tokens the session spent with the report's, one entry per message.

    Each message goes to the worklog span the session was in when it was written, or to the session
    alone outside every span. A session slate has not seen is recorded. Reporting the same messages
    again changes nothing and warns, so a session reports as often as it likes.
    """
    name_agent_session(id)
    now = dt.datetime.now(dt.UTC)
    agent = replace_usage(session, id, request, now, effects)
    session.commit()
    session.refresh(agent)
    return effects.answer(schemas.SessionWritten, session=session_view(session, agent, now))
