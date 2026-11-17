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
from sqlalchemy import select

from slate.database.base import Base

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


def held_worklog_flow(session_id: str) -> ColumnElement[str | None]:
    """The flow of the worklog `session_id`'s open span is in, null where it is in none.

    The tables are read from the metadata, because the models import this module's caller.
    """
    spans, worklogs = Base.metadata.tables['worklog_spans'], Base.metadata.tables['worklogs']
    query = (
        select(worklogs.c.flow_id)
        .join_from(spans, worklogs, spans.c.worklog_id == worklogs.c.id)
        .where(spans.c.session_id == session_id, spans.c.ended_ts.is_(None))
    )
    return query.scalar_subquery()


def writers_flow() -> ColumnElement[str]:
    """The SQL that resolves the current request's flow, by the order this module names."""
    fallback = flow_or_none(baggage.get_baggage(FLOW_ID_MEMBER)) or request_trace_flow()
    session_id = baggage.get_baggage(SESSION_ID_MEMBER)
    if isinstance(session_id, str) and session_id:
        return func.coalesce(held_worklog_flow(session_id), fallback)
    return literal(fallback)
