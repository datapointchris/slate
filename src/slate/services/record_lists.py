"""Records of every kind in one list, narrowed by the words each one's title or description holds.

Every kind is listed whatever its status, because the record a reader remembers may be the one
that closed. A promoted idea lists among the closed, since its work is its initiative's.
"""

from __future__ import annotations

import datetime as dt

from sqlalchemy import Select
from sqlalchemy import case
from sqlalchemy import literal
from sqlalchemy import null
from sqlalchemy import or_
from sqlalchemy import select
from sqlalchemy import union_all
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.record import Kind
from slate.services.issue_readiness import NO_PRIORITY_URGENCY
from slate.services.open_records import is_open
from slate.services.row_limit import apply_row_limit


def kind_query(kind: Kind, words: list[str]) -> Select[int, str, str, str | None, int, dt.datetime, bool]:
    """Each record of `kind` whose title or description holds every word, in any order, and whether it is closed."""
    match kind:
        case Kind.ISSUE:
            title, description = models.Issue.title, models.Issue.description
            query = select(
                models.Issue.number,
                literal(kind.value),
                title,
                models.Issue.status,
                models.Issue.priority,
                models.Issue.created_ts,
                ~is_open(kind),
            )
        case Kind.INITIATIVE:
            title, description = models.Initiative.name, models.Initiative.description
            query = select(
                models.Initiative.number,
                literal(kind.value),
                title,
                models.Initiative.status,
                models.Initiative.priority,
                models.Initiative.created_ts,
                ~is_open(kind),
            )
        case Kind.IDEA:
            title, description = models.Idea.title, models.Idea.description
            query = select(
                models.Idea.number, literal(kind.value), title, null(), models.Idea.priority, models.Idea.created_ts, ~is_open(kind)
            )
    for word in words:
        query = query.where(or_(title.icontains(word, autoescape=True), description.icontains(word, autoescape=True)))
    return query


def listed_records(session: Session, search: str | None, kinds: list[Kind], limit: int | None) -> list[schemas.RecordListed]:
    """Unclosed first, then the most urgent, then the newest."""
    words = (search or '').split()
    listed = union_all(*(kind_query(kind, words) for kind in kinds)).subquery()
    number, kind, title, record_status, priority, created_ts, closed = listed.c
    query = select(number, kind, title, record_status, priority, created_ts).order_by(
        closed,
        case((priority == 0, NO_PRIORITY_URGENCY), else_=priority),
        number.desc(),
    )
    return [
        schemas.RecordListed(number=row[0], kind=row[1], title=row[2], status=row[3], priority=row[4], created_ts=row[5])
        for row in session.execute(apply_row_limit(query, limit)).all()
    ]
