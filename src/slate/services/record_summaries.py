"""One line for each record of any kind, read from its kind's table by number."""

from __future__ import annotations

from collections.abc import Iterable

from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas


def record_summaries(session: Session, numbers: Iterable[int]) -> dict[int, schemas.RecordSummary]:
    wanted = sorted(set(numbers))
    if not wanted:
        return {}
    summaries: dict[int, schemas.RecordSummary] = {}
    issues = select(models.Issue.number, models.Issue.title, models.Issue.status).where(models.Issue.number.in_(wanted))
    for number, title, status in session.execute(issues).all():
        summaries[number] = schemas.RecordSummary(number=number, kind=models.Issue.RECORD_KIND, title=title, status=status)
    initiatives = select(models.Initiative.number, models.Initiative.name, models.Initiative.status).where(
        models.Initiative.number.in_(wanted)
    )
    for number, name, status in session.execute(initiatives).all():
        summaries[number] = schemas.RecordSummary(number=number, kind=models.Initiative.RECORD_KIND, title=name, status=status)
    return summaries
