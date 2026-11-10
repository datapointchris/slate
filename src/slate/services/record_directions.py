"""The directions each idea and initiative is in, the refusal of a number naming none, and the ones an issue reads as.

An idea's and an initiative's directions are their rows in `record_directions`. An issue reads its
initiative's, derived on every read, so placing an initiative in a direction or moving an issue
between initiatives changes what the issue reads with nothing stored on the issue.
"""

from __future__ import annotations

from collections import defaultdict
from collections.abc import Collection
from collections.abc import Iterable
from collections.abc import Sequence
from uuid import UUID

from fastapi import HTTPException
from fastapi import status
from sqlalchemy import ColumnElement
from sqlalchemy import Select
from sqlalchemy import delete
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.record import Kind
from slate.services.names_near import names_near
from slate.services.record_refs import direction_names
from slate.services.record_refs import find_direction
from slate.services.record_summaries import record_summaries


def unknown_directions(session: Session, unknown: Sequence[int | str]) -> HTTPException:
    """The refusal of references naming no direction, naming every active direction and the names each typed one comes near."""
    active = session.execute(
        select(models.Direction.number, models.Direction.name).where(models.Direction.status == 'active').order_by(models.Direction.number)
    ).all()
    named = ', '.join(f'#{ref}' if isinstance(ref, int) else repr(ref) for ref in unknown)
    listed = ', '.join(f'#{number} {name}' for number, name in active) or 'none yet'
    detail = f'{named} names no direction. The active directions: {listed}.'
    known = direction_names(session)
    near = list(dict.fromkeys(close for ref in unknown if isinstance(ref, str) for close in names_near(ref, known)))
    if near:
        detail += f' Near what was typed: {", ".join(near)}.'
    detail += ' `slate directions create` adds a direction.'
    return HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail=detail)


def resolve_directions(session: Session, numbers: Iterable[int]) -> list[models.Direction]:
    """Every numbered direction a write sets, refusing the whole set when any number names none.

    Each is locked `FOR KEY SHARE` until the write commits, so a delete racing it waits rather than
    failing the membership's key.
    """
    wanted = list(dict.fromkeys(numbers))
    if not wanted:
        return []
    query = select(models.Direction).where(models.Direction.number.in_(wanted)).with_for_update(key_share=True)
    found = {direction.number: direction for direction in session.scalars(query)}
    if unknown := [number for number in wanted if number not in found]:
        raise unknown_directions(session, unknown)
    return [found[number] for number in wanted]


def set_record_directions(session: Session, number: int, directions: Iterable[models.Direction]) -> None:
    """Make the record's directions exactly `directions`, leaving each row that stays, so its history holds."""
    wanted = {direction.id for direction in directions}
    held = set(session.scalars(select(models.RecordDirection.direction_id).where(models.RecordDirection.record_number == number)))
    if gone := held - wanted:
        session.execute(
            delete(models.RecordDirection).where(
                models.RecordDirection.record_number == number, models.RecordDirection.direction_id.in_(sorted(gone))
            )
        )
    session.add_all(models.RecordDirection(record_number=number, direction_id=direction_id) for direction_id in sorted(wanted - held))


def directions_of(session: Session, numbers: Collection[int]) -> dict[int, list[schemas.DirectionSummary]]:
    """Each record's directions by its number, in number order. A record in none is left out."""
    if not numbers:
        return {}
    rows = session.execute(
        select(models.RecordDirection.record_number, models.Direction.number, models.Direction.name)
        .join(models.Direction, models.Direction.id == models.RecordDirection.direction_id)
        .where(models.RecordDirection.record_number.in_(list(numbers)))
        .order_by(models.Direction.number)
    )
    directions: dict[int, list[schemas.DirectionSummary]] = defaultdict(list)
    for member, number, name in rows:
        directions[member].append(schemas.DirectionSummary(number=number, name=name))
    return dict(directions)


def issue_directions(session: Session, issues: Collection[models.Issue]) -> dict[int, list[schemas.DirectionSummary]]:
    """Each issue's directions by its number: its initiative's. Each issue's `initiative` is read, so load it with the issue."""
    initiative_of = {issue.number: issue.initiative.number for issue in issues if issue.initiative is not None}
    held = directions_of(session, set(initiative_of.values()))
    return {issue.number: held.get(initiative_of[issue.number], []) if issue.number in initiative_of else [] for issue in issues}


def filtered_direction(session: Session, ref: int | str) -> models.Direction:
    """The direction a filter names by number or exact name, refused as a write's would be."""
    direction = find_direction(session, ref)
    if direction is None:
        raise unknown_directions(session, [ref])
    return direction


def in_direction(session: Session, kind: Kind, ref: int | str) -> ColumnElement[bool]:
    """Whether a record of `kind` is in the direction: an idea or an initiative itself, an issue through its initiative."""
    direction = filtered_direction(session, ref)
    members = select(models.RecordDirection.record_number).where(models.RecordDirection.direction_id == direction.id)
    if kind == Kind.ISSUE:
        return models.Issue.initiative_id.in_(select(models.Initiative.id).where(models.Initiative.number.in_(members)))
    return models.KIND_MODELS[kind].number.in_(members)


def narrowed_to_direction(query: Select, kind: Kind, ref: int | str | None, session: Session) -> Select:
    return query if ref is None else query.where(in_direction(session, kind, ref))


def members_of(session: Session, direction_ids: Collection[UUID]) -> dict[UUID, list[schemas.RecordSummary]]:
    """Each direction's ideas and initiatives by its id, in number order, whatever their status."""
    if not direction_ids:
        return {}
    rows = session.execute(
        select(models.RecordDirection.direction_id, models.RecordDirection.record_number)
        .where(models.RecordDirection.direction_id.in_(list(direction_ids)))
        .order_by(models.RecordDirection.record_number)
    ).all()
    summaries = record_summaries(session, [number for _, number in rows])
    members: dict[UUID, list[schemas.RecordSummary]] = defaultdict(list)
    for direction_id, number in rows:
        members[direction_id].append(summaries[number])
    return dict(members)
