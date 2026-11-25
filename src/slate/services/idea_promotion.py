"""Turn an idea into an initiative or an issue under its own number, in one transaction.

This is class table inheritance's subtype change, as Jira and Azure Boards change an item's type.
The `ideas` row goes, the `records` row takes the new kind, and the new kind's row takes the idea's
number, `id` and `created_ts`. Every other row keyed on the number stays where it is, and so do the
rank, the pin and the flow, which live on `records`. The kind tables reference `records` on
`(number, kind)` with no `ON UPDATE`, so the idea's row goes before the kind changes, and the new
row comes after.
"""

from __future__ import annotations

import datetime as dt
from dataclasses import dataclass
from uuid import UUID

import structlog
from fastapi import HTTPException
from fastapi import status
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate.api.exceptions import NotFoundException
from slate.api.exceptions import with_article
from slate.models.record import Kind
from slate.services.record_lookup import number_holding_id
from slate.services.record_lookup import parse_uuid
from slate.services.record_lookup import record_number
from slate.services.record_lookup import resolve_idea

logger = structlog.get_logger()


@dataclass(frozen=True)
class PromotedIdea:
    """The idea as it stood, which the kind row inserted in its place keeps or starts from."""

    record: models.Record
    id: UUID
    title: str
    description: str | None
    priority: int
    created_ts: dt.datetime


def in_place_of(promoted: PromotedIdea | None) -> dict[str, object]:
    """What a kind row's constructor takes to sit in the idea's place: its record, `id` and `created_ts`. Nothing for a new record."""
    if promoted is None:
        return {}
    return {'record': promoted.record, 'id': promoted.id, 'created_ts': promoted.created_ts}


def lock_idea(session: Session, ref: str) -> models.Idea:
    """The idea `ref` names, with its `records` row locked `FOR UPDATE` until the transaction ends.

    Two promotions of one idea then run one after the other. The second reads the kind the first
    committed, and answers 409 as a number that another kind holds does.
    """
    number = record_number(ref)
    if number is None and (idea_id := parse_uuid(ref)) is not None:
        number = number_holding_id(session, idea_id)
    if number is None:
        number = resolve_idea(session, ref).number
    locked = select(models.Record).where(models.Record.number == number).with_for_update().execution_options(populate_existing=True)
    record = session.scalar(locked)
    if record is None:
        raise NotFoundException('idea', ref, logger)
    if record.kind != Kind.IDEA:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT, detail=f'#{number} is {with_article(record.kind)}; only an idea is promoted'
        )
    return session.scalars(select(models.Idea).where(models.Idea.number == number)).one()


def turn_idea_into(session: Session, idea: models.Idea, kind: Kind, promoted_by: str, now: dt.datetime) -> PromotedIdea:
    """Delete the idea's row and record its number as `kind`, promoted at `now` by `promoted_by`.

    The caller inserts the `kind` row from what this answers, before the transaction commits.
    """
    promoted = PromotedIdea(idea.record, idea.id, idea.title, idea.description, idea.priority, idea.created_ts)
    session.delete(idea)
    session.flush()
    promoted.record.kind = kind
    promoted.record.promoted_ts, promoted.record.promoted_by = now, promoted_by
    session.flush()
    return promoted
