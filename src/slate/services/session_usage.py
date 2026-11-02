"""Replace a session's tokens from its report, each message placed in the span it was spent in, and sum them back.

A message belongs to the span whose interval holds its `written_ts`: from the span's start until it
ended, or until its worklog's claim stopped holding when the span outlived the claim. A message no
span holds is the session's alone. A session's spans never overlap, so each message lands once.

A report names every message the session has spent tokens on. Each one it names is upserted, and a
row is written only when something the report carries for it differs from what slate holds. Each
row it leaves out is deleted. So reporting the same transcript again writes nothing.

A report replaces only a report it read at least as far as, transcript by transcript. It changes
nothing when its newest message from the session's own transcript, or from any subagent's slate
holds, is older than the newest slate holds there, or missing. A message's output never shrinks
either. A transcript only grows, so a smaller read is an earlier one, or one that failed partway.
"""

from __future__ import annotations

import datetime as dt
from collections.abc import Callable
from collections.abc import Iterable
from collections.abc import Sequence

import structlog
from sqlalchemy import Text
from sqlalchemy import all_
from sqlalchemy import and_
from sqlalchemy import any_
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


def span_placer(spans: Iterable[models.WorklogSpan], now: dt.datetime) -> Callable[[dt.datetime], int | None]:
    """A function giving the id of the span holding a moment, or None for a moment outside every span."""
    intervals = [(span.started_ts, span.held_until(now), span.id) for span in spans]

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
    """Make the session's messages the report's, each in the span it was spent in.

    The session's row is locked first, so two reports of one session, such as a turn's and the
    session's last, apply one after the other, and the one that read less changes nothing. A message
    another session reported first stays that session's.
    """
    record_session(session, session_id, report.host)
    agent = session.scalars(select(models.AgentSession).where(models.AgentSession.id == session_id).with_for_update()).one()
    newest_held = (
        select(models.MessageTokens.subagent, func.max(models.MessageTokens.written_ts))
        .where(models.MessageTokens.session_id == session_id)
        .group_by(models.MessageTokens.subagent)
    )
    held_through: dict[str | None, dt.datetime] = {subagent: newest for subagent, newest in session.execute(newest_held)}
    read_through: dict[str | None, dt.datetime] = {}
    for message in report.messages:
        read_through[message.subagent] = max(message.written_ts, read_through.get(message.subagent, message.written_ts))
    short = {
        transcript: held for transcript, held in held_through.items() if transcript not in read_through or read_through[transcript] < held
    }
    if short:
        effects.warn(WriteEffectCode.USAGE_STALE, stale_message(agent.id, short, read_through, effects))
        return agent
    keys = bindparam('report_keys', [message.key for message in report.messages], type_=ARRAY(Text))
    held_elsewhere = session.execute(
        select(models.MessageTokens.session_id, func.count().label('messages'))
        .where(models.MessageTokens.key == any_(keys), models.MessageTokens.session_id != session_id)
        .group_by(models.MessageTokens.session_id)
        .order_by(models.MessageTokens.session_id)
    ).all()
    if held_elsewhere:
        effects.warn(WriteEffectCode.MESSAGES_HELD_BY_ANOTHER_SESSION, held_elsewhere_message(session_id, held_elsewhere))
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
            index_elements=['key'],
            set_={name: reported.excluded[name] for name in REPORTED_COLUMNS},
            where=and_(
                stored.session_id == reported.excluded.session_id,
                stored.output_tokens <= reported.excluded.output_tokens,
                or_(*(stored[name].is_distinct_from(reported.excluded[name]) for name in REPORTED_COLUMNS)),
            ),
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
        return f'Session {agent.id} reported no messages it holds, and slate holds none for it. Nothing changed.'
    reported = effects.moment(agent.usage_reported_ts)
    return f'Session {agent.id} reported the same {messages} messages it reported at {reported}. Nothing changed.'


def stale_message(
    session_id: str, short: dict[str | None, dt.datetime], read_through: dict[str | None, dt.datetime], effects: WriteEffects
) -> str:
    """Name each transcript the report read less of than slate holds: the session's own, then each subagent's."""
    parts = []
    for transcript in sorted(short, key=lambda subagent: (subagent is not None, subagent or '')):
        name = 'its own transcript' if transcript is None else f"subagent {transcript}'s"
        read = read_through.get(transcript)
        reached = 'none of it' if read is None else f'through {effects.moment(read)}'
        parts.append(f'{name} {reached}, where slate holds it through {effects.moment(short[transcript])}')
    return f'Session {session_id} reported less than an earlier report read, so nothing changed: {"; ".join(parts)}.'


def held_elsewhere_message(session_id: str, held_elsewhere: Sequence) -> str:
    holders = ', '.join(f'{row.messages} by session {row.session_id}' for row in held_elsewhere)
    return f'Session {session_id} reported messages another session reported first, so they count there: {holders}.'


def tokens_of(row) -> schemas.TokensByKind:
    return schemas.TokensByKind(**{kind: getattr(row, kind) for kind in TOKEN_COLUMNS})


def summed(parts: Iterable[schemas.TokensByKind | None]) -> schemas.TokensByKind | None:
    """Each kind summed over the parts that hold a count, or None when none does."""
    counted = [part for part in parts if part is not None]
    if not counted:
        return None
    return schemas.TokensByKind(**{kind: sum(getattr(part, kind) for part in counted) for kind in TOKEN_COLUMNS})


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
    """The session with its tokens in all, in each span, and outside every worklog, as it last reported them.

    A sum over no reported message is None, so a span nobody reported for never reads as having spent nothing.
    """
    query = (
        select(models.MessageTokens.span_id, func.count().label('messages'), *TOKEN_SUMS)
        .where(models.MessageTokens.session_id == agent.id)
        .group_by(models.MessageTokens.span_id)
    )
    rows = session.execute(query).all()
    by_span = {row.span_id: tokens_of(row) for row in rows}
    spans = [
        schemas.SessionSpan(
            worklog=span.worklog.name,
            record_number=span.worklog.record_number,
            started_ts=span.started_ts,
            ended_ts=span.held_until(now),
            tokens=by_span.get(span.id),
        )
        for span in session_spans(session, agent.id)
    ]
    return schemas.AgentSession(
        id=agent.id,
        host=agent.host,
        first_seen_ts=agent.first_seen_ts,
        usage_reported_ts=agent.usage_reported_ts,
        message_count=sum(row.messages for row in rows),
        tokens=summed(by_span.values()),
        tokens_outside_worklogs=by_span.get(None),
        spans=spans,
    )
