"""What each initiative waits on, which initiatives wait on a record, and which of those waits hold now.

An initiative waits on an initiative or an issue through `initiative_dependencies`. While both it
and the record it waits on are open, as `open_records.is_open` reads them, every unclosed issue in
it is blocked. A someday initiative counts as open, so its wait holds too.
"""

from __future__ import annotations

from collections import defaultdict
from collections.abc import Collection
from dataclasses import dataclass
from uuid import UUID

from sqlalchemy import func
from sqlalchemy import or_
from sqlalchemy import select
from sqlalchemy.orm import Session
from sqlalchemy.orm import aliased

from slate import models
from slate import schemas
from slate.models.record import Kind
from slate.services.open_records import is_open

Wait = models.InitiativeDependency


@dataclass(frozen=True)
class HoldingWait:
    """A wait that blocks its initiative's unclosed issues now. `depends_on_initiative_id` is set where an initiative is waited on."""

    initiative_id: UUID
    initiative_number: int
    depends_on_number: int
    depends_on_initiative_id: UUID | None


def holding_waits(session: Session) -> list[HoldingWait]:
    """Every wait whose initiative and whose record are both open, in one query."""
    waiter = aliased(models.Initiative)
    waited_on = aliased(models.Initiative)
    query = (
        select(Wait.initiative_id, waiter.number, Wait.depends_on_number, waited_on.id)
        .join(waiter, waiter.id == Wait.initiative_id)
        .outerjoin(models.Issue, models.Issue.number == Wait.depends_on_number)
        .outerjoin(waited_on, waited_on.number == Wait.depends_on_number)
        .where(is_open(Kind.INITIATIVE, waiter), or_(is_open(Kind.ISSUE), is_open(Kind.INITIATIVE, waited_on)))
    )
    return [HoldingWait(*row) for row in session.execute(query).all()]


def waited_on(session: Session, initiative_ids: Collection[UUID]) -> dict[UUID, list[schemas.RecordSummary]]:
    """What each initiative waits on, open or closed, by number."""
    if not initiative_ids:
        return {}
    query = (
        select(
            Wait.initiative_id,
            models.Record.number,
            models.Record.kind,
            func.coalesce(models.Issue.title, models.Initiative.name, models.Idea.title),
            func.coalesce(models.Issue.status, models.Initiative.status),
        )
        .join(models.Record, models.Record.number == Wait.depends_on_number)
        .outerjoin(models.Issue, models.Issue.number == Wait.depends_on_number)
        .outerjoin(models.Initiative, models.Initiative.number == Wait.depends_on_number)
        .outerjoin(models.Idea, models.Idea.number == Wait.depends_on_number)
        .where(Wait.initiative_id.in_(initiative_ids))
        .order_by(models.Record.number)
    )
    waits: dict[UUID, list[schemas.RecordSummary]] = defaultdict(list)
    for initiative_id, number, kind, title, status in session.execute(query).all():
        waits[initiative_id].append(schemas.RecordSummary(number=number, kind=kind, title=title, status=status))
    return waits


def waiting_initiatives(session: Session, numbers: Collection[int]) -> dict[int, list[schemas.RecordSummary]]:
    """Each initiative waiting on a record, by the record's number."""
    if not numbers:
        return {}
    query = (
        select(Wait.depends_on_number, models.Initiative.number, models.Initiative.name, models.Initiative.status)
        .join(models.Initiative, models.Initiative.id == Wait.initiative_id)
        .where(Wait.depends_on_number.in_(numbers))
        .order_by(models.Initiative.number)
    )
    waiting: dict[int, list[schemas.RecordSummary]] = defaultdict(list)
    for waited_on_number, number, name, status in session.execute(query).all():
        waiting[waited_on_number].append(
            schemas.RecordSummary(number=number, kind=models.Initiative.RECORD_KIND, title=name, status=status)
        )
    return waiting
