"""The work a request touches, named on its server span, so one filter finds every trace that touched it.

`slate.record.number` is the one record a route is about: the record in its path, in its body, or
the one it created. A route names it once, where it resolves it, and never for a record it only
references, such as a dependency's target or the parent an issue files under. Every other record a
write changed reaches the span as an event, from `WriteEffects.answer`.

Each setter writes to the current span. A sync handler and its dependencies run in a thread that
copies the request's context, so the current span there is the request's server span.
"""

from opentelemetry import trace

from slate import models

RECORD_NUMBER = 'slate.record.number'
WORKLOG = 'slate.worklog'
# The convention's name for a session, and the one a Claude Code session's own telemetry carries.
AGENT_SESSION = 'session.id'


def name_record(number: int) -> None:
    trace.get_current_span().set_attribute(RECORD_NUMBER, number)


def name_worklog(worklog: models.Worklog) -> None:
    """The worklog alone. A comment's worklog can be a go at another record than the one commented on."""
    trace.get_current_span().set_attribute(WORKLOG, worklog.name)


def name_agent_session(session_id: str | None) -> None:
    if session_id:
        trace.get_current_span().set_attribute(AGENT_SESSION, session_id)
