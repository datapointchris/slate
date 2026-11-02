"""One line for each record of any kind, read from its kind's table by number. An idea has no status."""

from __future__ import annotations

from collections.abc import Iterable

from sqlalchemy import Select
from sqlalchemy import null
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.record import Kind


def summary_query(kind: Kind, numbers: list[int]) -> Select[int, str, str | None]:
    """Each numbered record of `kind` as its number, its title or name, and its status."""
    match kind:
        case Kind.ISSUE:
            return select(models.Issue.number, models.Issue.title, models.Issue.status).where(models.Issue.number.in_(numbers))
        case Kind.INITIATIVE:
            return select(models.Initiative.number, models.Initiative.name, models.Initiative.status).where(
                models.Initiative.number.in_(numbers)
            )
        case Kind.IDEA:
            return select(models.Idea.number, models.Idea.title, null()).where(models.Idea.number.in_(numbers))


def record_summaries(session: Session, numbers: Iterable[int]) -> dict[int, schemas.RecordSummary]:
    wanted = sorted(set(numbers))
    if not wanted:
        return {}
    return {
        number: schemas.RecordSummary(number=number, kind=kind, title=title, status=status)
        for kind in Kind
        for number, title, status in session.execute(summary_query(kind, wanted)).all()
    }
