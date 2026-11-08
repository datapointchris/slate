"""Records of every kind in one list, narrowed by the words each one's title or description holds.

A word that is a number, as `N` or `#N`, also matches the record with that number, and that
record lists first. Every kind is listed whatever its status, because the record a reader
remembers may be the one that closed. A promoted idea lists among the closed, since its work is
its initiative's.
"""

from __future__ import annotations

import datetime as dt
from typing import Any

from sqlalchemy import ColumnElement
from sqlalchemy import Select
from sqlalchemy import case
from sqlalchemy import literal
from sqlalchemy import null
from sqlalchemy import or_
from sqlalchemy import select
from sqlalchemy import union_all
from sqlalchemy.orm import InstrumentedAttribute
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.record import Kind
from slate.services.issue_readiness import NO_PRIORITY_URGENCY
from slate.services.open_records import is_open
from slate.services.record_refs import record_number
from slate.services.row_limit import apply_row_limit


def word_matches(word: str, number: InstrumentedAttribute[int], searched: tuple[InstrumentedAttribute[Any], ...]) -> ColumnElement[bool]:
    """Whether any `searched` column holds `word`, or `word` is `N` or `#N` and `number` is N."""
    named = record_number(word)
    if named is None:
        return or_(*(column.icontains(word, autoescape=True) for column in searched))
    digits = word.removeprefix('#')
    return or_(number == named, *(column.icontains(digits, autoescape=True) for column in searched))


def kind_query(kind: Kind, words: list[str]) -> Select[int, str, str, str | None, int, dt.datetime, bool]:
    """Each record of `kind` that every word matches, in any order, and whether it is closed."""
    searched: tuple[InstrumentedAttribute[Any], ...]
    number: InstrumentedAttribute[int]
    match kind:
        case Kind.ISSUE:
            number, title = models.Issue.number, models.Issue.title
            searched = (title, models.Issue.description)
            query = select(
                number,
                literal(kind.value),
                title,
                models.Issue.status,
                models.Issue.priority,
                models.Issue.created_ts,
                ~is_open(kind),
            )
        case Kind.INITIATIVE:
            number, title = models.Initiative.number, models.Initiative.name
            searched = (title, models.Initiative.summary, models.Initiative.description)
            query = select(
                number,
                literal(kind.value),
                title,
                models.Initiative.status,
                models.Initiative.priority,
                models.Initiative.created_ts,
                ~is_open(kind),
            )
        case Kind.IDEA:
            number, title = models.Idea.number, models.Idea.title
            searched = (title, models.Idea.description)
            query = select(number, literal(kind.value), title, null(), models.Idea.priority, models.Idea.created_ts, ~is_open(kind))
    for word in words:
        query = query.where(word_matches(word, number, searched))
    return query


def listed_records(session: Session, search: str | None, kinds: list[Kind], limit: int | None) -> list[schemas.RecordListed]:
    """A record a word names by number first, then the unclosed, then the most urgent, then the newest."""
    words = (search or '').split()
    named = [found for word in words if (found := record_number(word)) is not None]
    listed = union_all(*(kind_query(kind, words) for kind in kinds)).subquery()
    number, kind, title, record_status, priority, created_ts, closed = listed.c
    query = select(number, kind, title, record_status, priority, created_ts).order_by(
        ~number.in_(named),
        closed,
        case((priority == 0, NO_PRIORITY_URGENCY), else_=priority),
        number.desc(),
    )
    return [
        schemas.RecordListed(number=row[0], kind=row[1], title=row[2], status=row[3], priority=row[4], created_ts=row[5])
        for row in session.execute(apply_row_limit(query, limit)).all()
    ]
