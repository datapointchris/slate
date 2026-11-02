"""Every closed value a field of an issue or an initiative accepts."""

from fastapi import APIRouter
from fastapi import status
from sqlalchemy import select

from slate import models
from slate import schemas
from slate.api.callers import DbSession
from slate.models.initiative import INITIATIVE_STATUSES
from slate.models.issue import ISSUE_STATUSES
from slate.models.issue import ISSUE_TYPES
from slate.models.record import PRIORITIES
from slate.services.issue_views import label_views

router = APIRouter()


@router.get('', response_model=schemas.Vocabulary, status_code=status.HTTP_200_OK)
def read(session: DbSession):
    """Every value the closed fields accept, with the open issue count per label.

    Statuses and types come back in their lifecycle order rather than the
    table's, so a client can render them as given.
    """
    statuses = set(session.scalars(select(models.IssueStatus.name)).all())
    types = set(session.scalars(select(models.IssueType.name)).all())
    initiative_statuses = set(session.scalars(select(models.InitiativeStatus.name)).all())
    return schemas.Vocabulary(
        statuses=in_declared_order(statuses, ISSUE_STATUSES),
        types=in_declared_order(types, ISSUE_TYPES),
        priorities=[schemas.PriorityName(value=value, name=name) for value, name in PRIORITIES.items()],
        initiative_statuses=in_declared_order(initiative_statuses, INITIATIVE_STATUSES),
        labels=label_views(session),
    )


def in_declared_order(values: set[str], declared: list[str]) -> list[str]:
    """The declared values first in their order, then any the table holds beyond them."""
    return [v for v in declared if v in values] + sorted(values - set(declared))
