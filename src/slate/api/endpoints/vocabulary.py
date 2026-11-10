"""Every closed value a field accepts."""

from fastapi import APIRouter
from fastapi import status
from sqlalchemy import select

from slate import models
from slate import schemas
from slate.api.callers import DbSession
from slate.models.direction import DIRECTION_STATUSES
from slate.models.document import DOCUMENT_KINDS
from slate.models.initiative import INITIATIVE_STATUSES
from slate.models.issue import ISSUE_STATUSES
from slate.models.issue import ISSUE_TYPES
from slate.models.record import PRIORITIES
from slate.schemas.queue import QUEUE_STEPS
from slate.services.issue_views import label_views
from slate.services.record_tags import tag_views

router = APIRouter()


@router.get('', response_model=schemas.Vocabulary, status_code=status.HTTP_200_OK)
def read(session: DbSession):
    """Every value the closed fields accept, with the open issue count per label and the open record counts per tag.

    Statuses and types come back in their lifecycle order rather than the
    table's, so a client can render them as given.
    """
    issue_statuses = set(session.scalars(select(models.IssueStatus.name)).all())
    issue_types = set(session.scalars(select(models.IssueType.name)).all())
    initiative_statuses = set(session.scalars(select(models.InitiativeStatus.name)).all())
    direction_statuses = set(session.scalars(select(models.DirectionStatus.name)).all())
    document_kinds = set(session.scalars(select(models.DocumentKind.name)).all())
    worklog_steps = session.scalars(select(models.WorklogStep).order_by(models.WorklogStep.position)).all()
    return schemas.Vocabulary(
        issue_statuses=in_declared_order(issue_statuses, ISSUE_STATUSES),
        issue_types=in_declared_order(issue_types, ISSUE_TYPES),
        priorities=[schemas.PriorityName(value=value, name=name) for value, name in PRIORITIES.items()],
        initiative_statuses=in_declared_order(initiative_statuses, INITIATIVE_STATUSES),
        direction_statuses=in_declared_order(direction_statuses, DIRECTION_STATUSES),
        document_kinds=in_declared_order(document_kinds, DOCUMENT_KINDS),
        worklog_steps=[schemas.WorklogStep.model_validate(step) for step in worklog_steps],
        queue_steps=QUEUE_STEPS,
        labels=label_views(session),
        tags=tag_views(session),
    )


def in_declared_order(values: set[str], declared: list[str]) -> list[str]:
    """The declared values first in their order, then any the table holds beyond them."""
    return [v for v in declared if v in values] + sorted(values - set(declared))
