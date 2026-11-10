"""Whether a record of each kind is still open, the one rule every read across kinds asks."""

from __future__ import annotations

from typing import Any

from sqlalchemy import ColumnElement
from sqlalchemy import exists

from slate import models
from slate.models.direction import TERMINAL_DIRECTION_STATUSES
from slate.models.initiative import TERMINAL_INITIATIVE_STATUSES
from slate.models.issue import CLOSED_ISSUE_STATUSES
from slate.models.record import Kind


def is_open(kind: Kind, entity: Any = None) -> ColumnElement[bool]:
    """An issue not completed or canceled, an initiative or a direction not finished, an idea not promoted.

    A promoted idea's work is its initiative's, so the idea is done. `entity` is an alias of the
    kind's model, for a query joining that model twice.
    """
    match kind:
        case Kind.ISSUE:
            return (entity or models.Issue).status.not_in(CLOSED_ISSUE_STATUSES)
        case Kind.INITIATIVE:
            return (entity or models.Initiative).status.not_in(TERMINAL_INITIATIVE_STATUSES)
        case Kind.IDEA:
            return ~exists().where(models.Initiative.idea_id == (entity or models.Idea).id)
        case Kind.DIRECTION:
            return (entity or models.Direction).status.not_in(TERMINAL_DIRECTION_STATUSES)
