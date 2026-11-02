"""The name each worklog id reads as, for a row that references the worklog it was written in."""

from collections.abc import Iterable

from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models


def worklog_names(session: Session, ids: Iterable[int | None]) -> dict[int, str]:
    wanted = {worklog_id for worklog_id in ids if worklog_id is not None}
    if not wanted:
        return {}
    rows = session.execute(
        select(models.Worklog.id, models.Worklog.record_number, models.Worklog.sequence).where(models.Worklog.id.in_(wanted))
    )
    return {worklog_id: f'{number}.{sequence}' for worklog_id, number, sequence in rows}
