"""The work a request touches, named on its server span and on its request line.

`slate.record.number` is the one record a route is about: the record in its path, in its body, the
one a filter narrows a list to, or the one it created. A route names it once, where it resolves it,
and never for a record it only references, such as a dependency's target or the parent an issue
files under. Every other record a write changed reaches the span as an event, from
`WriteEffects.answer`.

Each setter writes to the current span and to the dict `open_named_work` gave the request. The
request line reads the dict, because a span the sampler dropped records nothing. A sync handler runs
in a thread on a copy of the request's context, so a value it bound there would never reach the
middleware. The dict is one object both contexts hold.
"""

from contextvars import ContextVar

from opentelemetry import trace

from slate import models

RECORD_NUMBER = 'slate.record.number'
WORKLOG = 'slate.worklog'
# The convention's name for a session, and the one a Claude Code session's own telemetry carries.
AGENT_SESSION = 'session.id'

# The request line's field for each attribute.
LOG_FIELDS = {RECORD_NUMBER: 'record', WORKLOG: 'worklog', AGENT_SESSION: 'session'}

_named_work: ContextVar[dict[str, str | int] | None] = ContextVar('named_work', default=None)


def open_named_work() -> dict[str, str | int]:
    """A fresh dict for this request's named work, keyed by the request line's field names."""
    named: dict[str, str | int] = {}
    _named_work.set(named)
    return named


def _name(attribute: str, value: str | int) -> None:
    trace.get_current_span().set_attribute(attribute, value)
    if (named := _named_work.get()) is not None:
        named[LOG_FIELDS[attribute]] = value


def name_record(number: int) -> None:
    _name(RECORD_NUMBER, number)


def name_worklog(worklog: models.Worklog) -> None:
    """The worklog alone. A comment's worklog can be a go at another record than the one commented on."""
    _name(WORKLOG, worklog.name)


def name_agent_session(session_id: str | None) -> None:
    if session_id:
        _name(AGENT_SESSION, session_id)
