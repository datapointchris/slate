"""Each record's checked steps and its stage, derived on every read from the worklogs that performed them.

A step is checked on a record once a worklog naming it ended by any outcome but `expired`. A claim
that ran out ended nobody's go deliberately, so it checks nothing. The stage is the furthest checked
step by `worklog_steps.position`, so the table's order decides it and no read stores it.
"""

from __future__ import annotations

from collections import defaultdict
from collections.abc import Collection
from typing import cast

from sqlalchemy import select
from sqlalchemy.dialects.postgresql import distinct_on
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.worklog import StepName

EXPIRED = 'expired'


def checked_steps(session: Session, numbers: Collection[int]) -> dict[int, list[schemas.CheckedStep]]:
    """Each record's checked steps in position order, each read from its latest worklog of that step, in one query."""
    if not numbers:
        return {}
    first_session = (
        select(models.WorklogSpan.session_id)
        .where(models.WorklogSpan.worklog_id == models.Worklog.id)
        .order_by(models.WorklogSpan.started_ts, models.WorklogSpan.id)
        .limit(1)
        .correlate(models.Worklog)
        .scalar_subquery()
    )
    query = (
        select(models.Worklog.record_number, models.Worklog.sequence, models.Worklog.step, models.Worklog.ended_ts, first_session)
        .join(models.WorklogStep, models.WorklogStep.name == models.Worklog.step)
        .where(models.Worklog.record_number.in_(numbers), models.Worklog.ended_ts.is_not(None), models.Worklog.outcome != EXPIRED)
        .ext(distinct_on(models.Worklog.record_number, models.WorklogStep.position))
        .order_by(models.Worklog.record_number, models.WorklogStep.position, models.Worklog.ended_ts.desc(), models.Worklog.sequence.desc())
    )
    steps: dict[int, list[schemas.CheckedStep]] = defaultdict(list)
    for number, sequence, step, ended_ts, first in session.execute(query).all():
        steps[number].append(
            schemas.CheckedStep(step=cast(StepName, step), worklog=f'{number}.{sequence}', ended_ts=ended_ts, session=first)
        )
    return steps


def stage_of(steps: list[schemas.CheckedStep]) -> schemas.RecordStage:
    """The furthest of `steps`, which `checked_steps` answers in position order, or `idea` before any."""
    return steps[-1].step if steps else 'idea'
