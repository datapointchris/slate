"""Whether a record of each kind is still open, the one rule every read across kinds asks."""

from __future__ import annotations

from typing import Any

from sqlalchemy import ColumnElement
from sqlalchemy import true

from slate import models
from slate.models.initiative import TERMINAL_INITIATIVE_STATUSES
from slate.models.issue import CLOSED_ISSUE_STATUSES
from slate.models.pursuit import TERMINAL_PURSUIT_STATUSES
from slate.models.record import Kind


def is_open(kind: Kind, entity: Any = None) -> ColumnElement[bool]:
    """An issue not completed or canceled, an initiative or a pursuit not finished, and every idea.

    An idea ends by turning into its initiative or issue, or by folding into other work, and either
    way its `ideas` row goes. `entity` is an alias of the kind's model, for a query joining that
    model twice.
    """
    match kind:
        case Kind.ISSUE:
            return (entity or models.Issue).status.not_in(CLOSED_ISSUE_STATUSES)
        case Kind.INITIATIVE:
            return (entity or models.Initiative).status.not_in(TERMINAL_INITIATIVE_STATUSES)
        case Kind.IDEA:
            return true()
        case Kind.PURSUIT:
            return (entity or models.Pursuit).status.not_in(TERMINAL_PURSUIT_STATUSES)
