"""Each initiative's checked steps and its stage, derived on every read from the worklogs that performed them.

A step is checked on a record once a worklog naming it ended because its go did: finished by hand,
released, or ended by its record completing or its idea being promoted. A claim that ran out ended
nobody's go, and a record dropped or canceled abandoned it, so neither checks a step. An initiative
also reads the steps checked on the idea promoted into it, because an idea is shaped before it has
an initiative's number. Steps come in `WORKLOG_STEPS` order, which `worklog_steps.position` mirrors,
and the stage is the furthest of them.
"""

from __future__ import annotations

from collections import defaultdict
from collections.abc import Collection
from collections.abc import Sequence
from typing import cast
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.dialects.postgresql import distinct_on
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.worklog import WORKLOG_STEPS
from slate.models.worklog import StepName

# The endings that stopped a go without its step being done.
UNPERFORMED_OUTCOMES = ('expired', 'dropped', 'canceled')


def checked_steps(session: Session, numbers: Collection[int]) -> dict[int, dict[str, schemas.CheckedStep]]:
    """Each record's checked steps by name, each read from its latest worklog of that step, in one query."""
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
        .where(
            models.Worklog.record_number.in_(numbers),
            models.Worklog.step.is_not(None),
            models.Worklog.ended_ts.is_not(None),
            models.Worklog.outcome.not_in(UNPERFORMED_OUTCOMES),
        )
        .ext(distinct_on(models.Worklog.record_number, models.Worklog.step))
        .order_by(models.Worklog.record_number, models.Worklog.step, models.Worklog.ended_ts.desc(), models.Worklog.sequence.desc())
    )
    steps: dict[int, dict[str, schemas.CheckedStep]] = defaultdict(dict)
    for number, sequence, step, ended_ts, first in session.execute(query).all():
        steps[number][step] = schemas.CheckedStep(
            step=cast(StepName, step), worklog=f'{number}.{sequence}', ended_ts=ended_ts, session=first
        )
    return steps


def initiative_steps(session: Session, initiatives: Sequence[models.Initiative]) -> dict[int, list[schemas.CheckedStep]]:
    """Each initiative's checked steps with its idea's, the later worklog where both checked one, in `WORKLOG_STEPS` order."""
    idea_ids = [initiative.idea_id for initiative in initiatives if initiative.idea_id is not None]
    idea_numbers: dict[UUID | None, int] = (
        dict(session.execute(select(models.Idea.id, models.Idea.number).where(models.Idea.id.in_(idea_ids))).all()) if idea_ids else {}
    )
    records = {
        initiative.number: [number for number in (initiative.number, idea_numbers.get(initiative.idea_id)) if number is not None]
        for initiative in initiatives
    }
    checked = checked_steps(session, [number for numbers in records.values() for number in numbers])
    steps: dict[int, list[schemas.CheckedStep]] = {}
    for initiative, numbers in records.items():
        latest: dict[str, schemas.CheckedStep] = {}
        for step in (step for number in numbers for step in checked.get(number, {}).values()):
            if step.step not in latest or step.ended_ts > latest[step.step].ended_ts:
                latest[step.step] = step
        steps[initiative] = sorted(latest.values(), key=lambda step: WORKLOG_STEPS.index(step.step))
    return steps


def stage_of(steps: list[schemas.CheckedStep]) -> schemas.RecordStage:
    """The furthest of `steps`, which `initiative_steps` answers in order, or `idea` before any."""
    return steps[-1].step if steps else 'idea'
