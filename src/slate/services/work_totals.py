"""Total the work over a record, a worklog, a repo and a window: its worklogs, sessions, links, messages and tokens by kind.

Every figure is derived on read and none is stored. A scope narrows which worklogs a total reaches:

- A record reaches its own worklogs and those of every record its work reaches through to, which
  `services/record_reach.py` decides.
- A worklog reaches itself.
- A repo reaches its issues' worklogs.

Named together, they narrow together. Naming none reaches every worklog and every message no
worklog held, so that total counts the time sessions spent outside every worklog.

A window narrows each figure to what happened inside it: a message by when it was written, a link
by when it was linked, and a session or worklog by whether its time overlapped the window. A
message has one moment, so a window's edge splits a span's tokens exactly.
"""

from __future__ import annotations

import datetime as dt
from collections import defaultdict
from collections.abc import Sequence

from sqlalchemy import ColumnElement
from sqlalchemy import Row
from sqlalchemy import Select
from sqlalchemy import func
from sqlalchemy import or_
from sqlalchemy import select
from sqlalchemy import union
from sqlalchemy.orm import Session
from sqlalchemy.sql.elements import SQLCoreOperations

from slate import models
from slate import schemas
from slate.models.worklog import WORKLOG_LINK_KINDS
from slate.models.worklog import with_worklog_claim
from slate.services.record_reach import within_reach
from slate.services.record_summaries import record_summaries
from slate.services.session_usage import TOKEN_SUMS
from slate.services.session_usage import summed
from slate.services.session_usage import tokens_of
from slate.services.time_window import TimeWindow
from slate.services.time_window import apply_time_window


def reached_by_repo(repo: str) -> ColumnElement[bool]:
    """Whether a worklog sits on an issue in the repo. An empty name reaches the issues on no repo."""
    on_repo = models.Issue.repo == repo if repo else models.Issue.repo.is_(None)
    return models.Worklog.record_number.in_(select(models.Issue.number).where(on_repo))


def scope_worklog_ids(session: Session, record: models.Record | None, worklog: models.Worklog | None, repo: str | None) -> Select | None:
    """The ids of the worklogs the scope reaches, or None when it names nothing and so reaches everything."""
    conditions = []
    if record is not None:
        conditions.append(within_reach(session, record, models.Worklog.record_number))
    if worklog is not None:
        conditions.append(models.Worklog.id == worklog.id)
    if repo is not None:
        conditions.append(reached_by_repo(repo))
    return select(models.Worklog.id).where(*conditions) if conditions else None


def overlapping(
    started: SQLCoreOperations[dt.datetime], held_until: SQLCoreOperations[dt.datetime | None], window: TimeWindow
) -> list[ColumnElement[bool]]:
    """The conditions for a stretch of time from `started` to `held_until`, null while it runs, to overlap the window."""
    conditions = []
    if window.until is not None:
        conditions.append(started < window.until)
    if window.since is not None:
        conditions.append(or_(held_until.is_(None), held_until > window.since))
    return conditions


def counted_messages(query: Select, worklog_ids: Select | None, window: TimeWindow) -> Select:
    """Narrow a query on `message_tokens` to the messages a total counts: in its worklogs' spans, and written in its window."""
    if worklog_ids is not None:
        query = query.join(models.WorklogSpan, models.MessageTokens.span_id == models.WorklogSpan.id).where(
            models.WorklogSpan.worklog_id.in_(worklog_ids)
        )
    return apply_time_window(query, models.MessageTokens.written_ts, window, zone=None)


def count_distinct(session: Session, *queries: Select) -> int:
    """How many distinct rows the queries select between them."""
    return session.scalar(select(func.count()).select_from(union(*queries).subquery())) or 0


def session_count(session: Session, worklog_ids: Select | None, window: TimeWindow, now: dt.datetime) -> int:
    """Sessions that were in one of the worklogs during the window, or spent a token the total counts."""
    in_worklogs = with_worklog_claim(select(models.WorklogSpan.session_id).join(models.WorklogSpan.worklog)).where(
        *overlapping(models.WorklogSpan.started_ts, models.WorklogSpan.held_until(now), window)
    )
    if worklog_ids is not None:
        in_worklogs = in_worklogs.where(models.WorklogSpan.worklog_id.in_(worklog_ids))
    spent = counted_messages(select(models.MessageTokens.session_id), worklog_ids, window)
    return count_distinct(session, in_worklogs, spent)


def worklog_count(session: Session, worklog_ids: Select | None, window: TimeWindow, now: dt.datetime) -> int:
    """Worklogs under way during the window, or that took a link inside it."""
    under_way = with_worklog_claim(select(models.Worklog.id)).where(
        *overlapping(models.Worklog.started_ts, models.Worklog.held_until(now), window)
    )
    linked = apply_time_window(select(models.WorklogLink.worklog_id), models.WorklogLink.linked_ts, window, zone=None)
    if worklog_ids is not None:
        under_way = under_way.where(models.Worklog.id.in_(worklog_ids))
        linked = linked.where(models.WorklogLink.worklog_id.in_(worklog_ids))
    return count_distinct(session, under_way, linked)


def link_counts(session: Session, worklog_ids: Select | None, window: TimeWindow) -> dict[str, int]:
    """Each link kind's distinct refs linked inside the window, with every kind present and zero where none was.

    A ref linked to two worklogs, such as a pull request two goes at one issue both pushed to, counts once.
    """
    query = select(models.WorklogLink.kind, func.count(models.WorklogLink.ref.distinct()).label('refs')).group_by(models.WorklogLink.kind)
    if worklog_ids is not None:
        query = query.where(models.WorklogLink.worklog_id.in_(worklog_ids))
    query = apply_time_window(query, models.WorklogLink.linked_ts, window, zone=None)
    counts = dict.fromkeys(WORKLOG_LINK_KINDS, 0)
    for kind, refs in session.execute(query):
        counts[kind] = refs
    return counts


def message_rows(session: Session, worklog_ids: Select | None, window: TimeWindow) -> Sequence[Row]:
    """The counted messages' count and tokens by kind, grouped by model and by whether no worklog held them."""
    outside = models.MessageTokens.span_id.is_(None).label('outside')
    query = select(models.MessageTokens.model, outside, func.count().label('messages'), *TOKEN_SUMS)
    return session.execute(counted_messages(query, worklog_ids, window).group_by(models.MessageTokens.model, outside)).all()


def model_tokens(rows: Sequence[Row]) -> list[schemas.ModelTokens]:
    """Each model's messages and tokens, by model name, with messages that name none last."""
    by_model: dict[str | None, list[Row]] = defaultdict(list)
    for row in rows:
        by_model[row.model].append(row)
    return [
        schemas.ModelTokens(
            model=model,
            message_count=sum(row.messages for row in parts),
            tokens=sum((tokens_of(row) for row in parts), schemas.TokensByKind()),
        )
        for model, parts in sorted(by_model.items(), key=lambda item: (item[0] is None, item[0] or ''))
    ]


def work_totals(
    session: Session,
    now: dt.datetime,
    window: TimeWindow,
    *,
    record: models.Record | None = None,
    worklog: models.Worklog | None = None,
    repo: str | None = None,
) -> schemas.WorkTotals:
    """The totals of the work the scope reaches inside the window, as of `now`.

    `now` decides where a span or worklog whose claim ran out stopped. Where the scope names no
    record, worklog or repo and its messages were reported, the part outside every worklog is a
    count, zero when none fell there.
    """
    worklog_ids = scope_worklog_ids(session, record, worklog, repo)
    rows = message_rows(session, worklog_ids, window)
    tokens = summed(tokens_of(row) for row in rows)
    outside = None
    if worklog_ids is None and tokens is not None:
        outside = sum((tokens_of(row) for row in rows if row.outside), schemas.TokensByKind())
    scope = schemas.TotalsScope(
        record=record_summaries(session, [record.number])[record.number] if record is not None else None,
        worklog=worklog.name if worklog is not None else None,
        repo=repo,
        since=window.since,
        until=window.until,
    )
    return schemas.WorkTotals(
        scope=scope,
        worklog_count=worklog_count(session, worklog_ids, window, now),
        session_count=session_count(session, worklog_ids, window, now),
        link_counts=link_counts(session, worklog_ids, window),
        message_count=sum(row.messages for row in rows),
        tokens=tokens,
        tokens_outside_worklogs=outside,
        tokens_by_model=model_tokens(rows),
    )
