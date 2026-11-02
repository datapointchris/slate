"""Replace a session's tokens from its report, each message placed in the span it was spent in, and sum them back.

A message belongs to the span whose interval holds its `written_ts`: from the span's start until it
ended, or until its worklog's claim stopped holding when the span outlived the claim. A message no
span holds is the session's alone. A session's spans never overlap, so each message lands once.

A report names every message the session has spent tokens on. Each one it names is upserted, and a
row is written only when its figures or its span changed. Each row it leaves out is deleted. So
reporting the same transcript again writes nothing.
"""

from __future__ import annotations

import datetime as dt
from collections.abc import Callable
from collections.abc import Iterable
from collections.abc import Sequence

import structlog
from sqlalchemy import Text
from sqlalchemy import all_
from sqlalchemy import bindparam
from sqlalchemy import delete
from sqlalchemy import func
from sqlalchemy import or_
from sqlalchemy import select
from sqlalchemy.dialects.postgresql import ARRAY
from sqlalchemy.dialects.postgresql import insert
from sqlalchemy.orm import Session
from sqlalchemy.orm import selectinload

from slate import models
from slate import schemas
from slate.services.worklogs import record_session
from slate.services.write_effects import WriteEffectCode
from slate.services.write_effects import WriteEffects

logger = structlog.get_logger()

# Each kind's column, by the name `TokensByKind` gives it.
TOKEN_COLUMNS = {
    'uncached_input': models.MessageTokens.uncached_input_tokens,
    'cache_write': models.MessageTokens.cache_write_tokens,
    'cache_read': models.MessageTokens.cache_read_tokens,
    'output': models.MessageTokens.output_tokens,
}
TOKEN_SUMS = tuple(func.coalesce(func.sum(column), 0).label(kind) for kind, column in TOKEN_COLUMNS.items())

# What a report sets on a message's row, so an upsert writes the row only when one of these changed.
REPORTED_COLUMNS = ('written_ts', 'model', 'subagent', *(column.key for column in TOKEN_COLUMNS.values()), 'span_id')


def span_end(span: models.WorklogSpan, now: dt.datetime) -> dt.datetime | None:
    """When the span stopped holding the session's time, as of `now`, or None while it still holds it.

    A span still open in a worklog whose claim ended or ran out stopped when the claim did, though
    no write has ended it yet.
    """
    if span.ended_ts is not None:
        return span.ended_ts
    claim = span.worklog.claim
    if claim is None:
        return None
    if claim.ended_ts is not None:
        return claim.ended_ts
    return claim.expires_ts if claim.is_expired(now) else None


def span_placer(spans: Iterable[models.WorklogSpan], now: dt.datetime) -> Callable[[dt.datetime], int | None]:
    """A function giving the id of the span holding a moment, or None for a moment outside every span."""
    intervals = [(span.started_ts, span_end(span, now), span.id) for span in spans]

    def span_holding(moment: dt.datetime) -> int | None:
        for started, ended, span_id in intervals:
            if started <= moment and (ended is None or moment < ended):
                return span_id
        return None

    return span_holding


def session_spans(session: Session, session_id: str) -> Sequence[models.WorklogSpan]:
    query = (
        select(models.WorklogSpan)
        .where(models.WorklogSpan.session_id == session_id)
        .options(selectinload(models.WorklogSpan.worklog).selectinload(models.Worklog.claim))
        .order_by(models.WorklogSpan.started_ts, models.WorklogSpan.id)
    )
    return session.scalars(query).all()


def replace_usage(
    session: Session, session_id: str, report: schemas.SessionUsageReport, now: dt.datetime, effects: WriteEffects
) -> models.AgentSession:
    """Make the session's messages exactly the report's, each in the span it was spent in.

    The session's row is locked first, so two reports of one session, such as a turn's and the
    session's last, replace one after the other rather than interleaving.
    """
    record_session(session, session_id, report.host)
    agent = session.scalars(select(models.AgentSession).where(models.AgentSession.id == session_id).with_for_update()).one()
    span_holding = span_placer(session_spans(session, session_id), now)
    rows = [
        {
            'session_id': session_id,
            'key': message.key,
            'written_ts': message.written_ts,
            'model': message.model,
            'subagent': message.subagent,
            **{column.key: getattr(message.tokens, kind) for kind, column in TOKEN_COLUMNS.items()},
            'span_id': span_holding(message.written_ts),
        }
        for message in report.messages
    ]
    written = 0
    if rows:
        reported = insert(models.MessageTokens)
        stored = models.MessageTokens.__table__.c
        upsert = reported.on_conflict_do_update(
            index_elements=['session_id', 'key'],
            set_={name: reported.excluded[name] for name in REPORTED_COLUMNS},
            where=or_(*(stored[name].is_distinct_from(reported.excluded[name]) for name in REPORTED_COLUMNS)),
        ).returning(models.MessageTokens.key)
        written = len(session.execute(upsert, rows).all())
    kept = bindparam('kept_keys', [row['key'] for row in rows], type_=ARRAY(Text))
    left_out = delete(models.MessageTokens).where(models.MessageTokens.session_id == session_id, models.MessageTokens.key != all_(kept))
    deleted = len(session.execute(left_out.returning(models.MessageTokens.key)).all())
    if written or deleted:
        agent.usage_reported_ts = now
        logger.info('session_usage_replaced', session_id=session_id, messages=len(rows), written=written, deleted=deleted)
    else:
        effects.warn(WriteEffectCode.USAGE_UNCHANGED, unchanged_message(agent, len(rows), effects))
    return agent


def unchanged_message(agent: models.AgentSession, messages: int, effects: WriteEffects) -> str:
    if agent.usage_reported_ts is None:
        return f'Session {agent.id} reported no messages, and slate holds none for it. Nothing changed.'
    reported = effects.moment(agent.usage_reported_ts)
    return f'Session {agent.id} reported the same {messages} messages it reported at {reported}. Nothing changed.'


def tokens_of(row) -> schemas.TokensByKind:
    return schemas.TokensByKind(**{kind: getattr(row, kind) for kind in TOKEN_COLUMNS})


def plus(left: schemas.TokensByKind, right: schemas.TokensByKind) -> schemas.TokensByKind:
    return schemas.TokensByKind(**{kind: getattr(left, kind) + getattr(right, kind) for kind in TOKEN_COLUMNS})


def tokens_by_span(session: Session, span_ids: Iterable[int]) -> dict[int, schemas.TokensByKind]:
    """Each span's tokens by kind, over the messages placed in it. A span with none is left out."""
    ids = list(span_ids)
    if not ids:
        return {}
    query = (
        select(models.MessageTokens.span_id, *TOKEN_SUMS)
        .where(models.MessageTokens.span_id.in_(ids))
        .group_by(models.MessageTokens.span_id)
    )
    return {row.span_id: tokens_of(row) for row in session.execute(query)}


def session_view(session: Session, agent: models.AgentSession, now: dt.datetime) -> schemas.AgentSession:
    """The session with its tokens in all, in each span, and outside every worklog, as it last reported them."""
    query = (
        select(models.MessageTokens.span_id, func.count().label('messages'), *TOKEN_SUMS)
        .where(models.MessageTokens.session_id == agent.id)
        .group_by(models.MessageTokens.span_id)
    )
    by_span = {row.span_id: row for row in session.execute(query)}
    total = schemas.TokensByKind()
    for row in by_span.values():
        total = plus(total, tokens_of(row))
    outside = by_span.get(None)
    spans = [
        schemas.SessionSpan(
            worklog=span.worklog.name,
            record_number=span.worklog.record_number,
            started_ts=span.started_ts,
            ended_ts=span.ended_ts,
            tokens=tokens_of(by_span[span.id]) if span.id in by_span else schemas.TokensByKind(),
        )
        for span in session_spans(session, agent.id)
    ]
    return schemas.AgentSession(
        id=agent.id,
        host=agent.host,
        first_seen_ts=agent.first_seen_ts,
        usage_reported_ts=agent.usage_reported_ts,
        messages=sum(row.messages for row in by_span.values()),
        tokens=total,
        tokens_outside_worklogs=tokens_of(outside) if outside is not None else schemas.TokensByKind(),
        spans=spans,
    )
