"""The flow a write is made under, which each row it creates and its audit row keep as `flow_id`.

A flow id is a W3C trace id: 32 lowercase hex characters, not all zeros. A value in any other form
reads as no flow, wherever it arrives from. A write's flow is the first of these it has:

1. the flow of the worklog the writing agent session is in, from the session's open span. The
   request names its session in the baggage member `SESSION_ID_MEMBER`.
2. the request's `FLOW_ID_MEMBER` baggage member
3. the request's trace id, or a fresh one where the request carries no trace

The session's worklog comes first because a session's baggage is fixed when it starts, and the
worklog it is in now names the work it does now.
"""

import re

from opentelemetry import baggage
from opentelemetry import trace
from opentelemetry.sdk.trace.id_generator import RandomIdGenerator
from opentelemetry.trace import format_trace_id
from sqlalchemy import ColumnElement
from sqlalchemy import func
from sqlalchemy import literal

FLOW_ID_MEMBER = 'fleet.flow.id'
SESSION_ID_MEMBER = 'fleet.session.id'

FLOW_ID_SHAPE = re.compile('[0-9a-f]{32}')
NO_FLOW = '0' * 32


def flow_or_none(value: object) -> str | None:
    """`value` where it is a flow id, else None."""
    if isinstance(value, str) and FLOW_ID_SHAPE.fullmatch(value) and value != NO_FLOW:
        return value
    return None


def request_trace_flow() -> str:
    """The current trace's id, or a fresh one where no trace is current."""
    span_context = trace.get_current_span().get_span_context()
    trace_id = span_context.trace_id if span_context.is_valid else RandomIdGenerator().generate_trace_id()
    return format_trace_id(trace_id)


def writers_flow() -> ColumnElement[str]:
    """The SQL that resolves the current request's flow, by the order this module names."""
    # Imported here because `models.record_change`, which the models import, calls this module.
    from slate.models.worklog import held_worklog_flow

    fallback = flow_or_none(baggage.get_baggage(FLOW_ID_MEMBER)) or request_trace_flow()
    session_id = baggage.get_baggage(SESSION_ID_MEMBER)
    if isinstance(session_id, str) and session_id:
        return func.coalesce(held_worklog_flow(session_id), fallback)
    return literal(fallback)
