"""Whether a record of each kind is still open, the one rule every read across kinds asks."""

from __future__ import annotations

from sqlalchemy import ColumnElement
from sqlalchemy import exists

from slate import models
from slate.models.initiative import TERMINAL_INITIATIVE_STATUSES
from slate.models.issue import CLOSED_ISSUE_STATUSES
from slate.models.record import Kind


def is_open(kind: Kind) -> ColumnElement[bool]:
    """An issue not completed or canceled, an initiative not finished, an idea not promoted.

    A promoted idea's work is its initiative's, so the idea is done.
    """
    match kind:
        case Kind.ISSUE:
            return models.Issue.status.not_in(CLOSED_ISSUE_STATUSES)
        case Kind.INITIATIVE:
            return models.Initiative.status.not_in(TERMINAL_INITIATIVE_STATUSES)
        case Kind.IDEA:
            return ~exists().where(models.Initiative.idea_id == models.Idea.id)
