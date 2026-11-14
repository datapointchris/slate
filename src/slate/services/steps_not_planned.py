"""The steps of its life each initiative will not run, and the write replacing the set.

A step with no row is planned, so an initiative nobody marked plans every step. `build` is never
one, because an initiative's issues are its build. The request schema refuses it by name, and a
check constraint holds it.
"""

from __future__ import annotations

from collections import defaultdict
from collections.abc import Collection
from collections.abc import Iterable
from typing import cast
from uuid import UUID

from sqlalchemy import delete
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate.models.worklog import WORKLOG_STEPS
from slate.models.worklog import StepName

NotPlanned = models.InitiativeStepNotPlanned


def steps_not_planned(session: Session, initiative_ids: Collection[UUID]) -> dict[UUID, list[StepName]]:
    """Each initiative's steps not planned by its id, in `WORKLOG_STEPS` order. One planning every step is left out."""
    if not initiative_ids:
        return {}
    rows = session.execute(select(NotPlanned.initiative_id, NotPlanned.step).where(NotPlanned.initiative_id.in_(list(initiative_ids))))
    steps: dict[UUID, list[StepName]] = defaultdict(list)
    for initiative_id, step in rows:
        steps[initiative_id].append(cast(StepName, step))
    return {initiative_id: sorted(marked, key=WORKLOG_STEPS.index) for initiative_id, marked in steps.items()}


def set_steps_not_planned(session: Session, initiative_id: UUID, steps: Iterable[StepName]) -> None:
    """Make the initiative's steps not planned exactly `steps`, leaving each row that stays, so its history holds."""
    wanted = set(steps)
    held = set(session.scalars(select(NotPlanned.step).where(NotPlanned.initiative_id == initiative_id)))
    if gone := held - wanted:
        session.execute(delete(NotPlanned).where(NotPlanned.initiative_id == initiative_id, NotPlanned.step.in_(sorted(gone))))
    session.add_all(NotPlanned(initiative_id=initiative_id, step=step) for step in sorted(wanted - held))
