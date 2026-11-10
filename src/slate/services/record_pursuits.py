"""The pursuits each idea and initiative is in, the refusal of a number naming none, and the ones an issue reads as.

An idea's and an initiative's pursuits are their rows in `record_pursuits`. An issue reads its
initiative's, derived on every read, so placing an initiative in a pursuit or moving an issue
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
from slate.models.pursuit import TERMINAL_PURSUIT_STATUSES
from slate.models.record import Kind
from slate.services.names_near import names_near
from slate.services.record_refs import find_pursuit
from slate.services.record_refs import pursuit_names
from slate.services.record_summaries import record_summaries


def unknown_pursuits(session: Session, unknown: Sequence[int | str]) -> HTTPException:
    """The refusal of references naming no pursuit, naming every active pursuit and the names each typed one comes near."""
    active = session.execute(
        select(models.Pursuit.number, models.Pursuit.name).where(models.Pursuit.status == 'active').order_by(models.Pursuit.number)
    ).all()
    named = ', '.join(f'#{ref}' if isinstance(ref, int) else repr(ref) for ref in unknown)
    listed = ', '.join(f'#{number} {name}' for number, name in active) or 'none yet'
    detail = f'{named} names no pursuit. The active pursuits: {listed}.'
    known = pursuit_names(session)
    near = list(dict.fromkeys(close for ref in unknown if isinstance(ref, str) for close in names_near(ref, known)))
    if near:
        detail += f' Near what was typed: {", ".join(near)}.'
    detail += ' `slate pursuits create` adds a pursuit.'
    return HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail=detail)


def resolve_pursuits(session: Session, numbers: Iterable[int]) -> list[models.Pursuit]:
    """Every numbered pursuit a write sets, refusing the whole set when any number names none.

    Each is locked `FOR KEY SHARE` until the write commits, so a delete racing it waits rather than
    failing the membership's key.
    """
    wanted = list(dict.fromkeys(numbers))
    if not wanted:
        return []
    query = select(models.Pursuit).where(models.Pursuit.number.in_(wanted)).with_for_update(key_share=True)
    found = {pursuit.number: pursuit for pursuit in session.scalars(query)}
    if unknown := [number for number in wanted if number not in found]:
        raise unknown_pursuits(session, unknown)
    return [found[number] for number in wanted]


def set_record_pursuits(session: Session, number: int, pursuits: Iterable[models.Pursuit]) -> None:
    """Make the record's pursuits exactly `pursuits`, leaving each row that stays, so its history holds.

    A completed or dropped pursuit is refused with 409 where the write would newly place the record in
    it. One the record is already in stays, so editing a record in a finished pursuit still works.
    """
    placed = list(pursuits)
    wanted = {pursuit.id for pursuit in placed}
    held = set(session.scalars(select(models.RecordPursuit.pursuit_id).where(models.RecordPursuit.record_number == number)))
    for pursuit in placed:
        if pursuit.id not in held and pursuit.status in TERMINAL_PURSUIT_STATUSES:
            raise HTTPException(
                status_code=status.HTTP_409_CONFLICT,
                detail=f'Pursuit {pursuit.name!r} is {pursuit.status}. Reopen it before placing work in it.',
            )
    if gone := held - wanted:
        session.execute(
            delete(models.RecordPursuit).where(
                models.RecordPursuit.record_number == number, models.RecordPursuit.pursuit_id.in_(sorted(gone))
            )
        )
    session.add_all(models.RecordPursuit(record_number=number, pursuit_id=pursuit_id) for pursuit_id in sorted(wanted - held))


def pursuits_of(session: Session, numbers: Collection[int]) -> dict[int, list[schemas.PursuitSummary]]:
    """Each record's pursuits by its number, in number order. A record in none is left out."""
    if not numbers:
        return {}
    rows = session.execute(
        select(models.RecordPursuit.record_number, models.Pursuit.number, models.Pursuit.name)
        .join(models.Pursuit, models.Pursuit.id == models.RecordPursuit.pursuit_id)
        .where(models.RecordPursuit.record_number.in_(list(numbers)))
        .order_by(models.Pursuit.number)
    )
    pursuits: dict[int, list[schemas.PursuitSummary]] = defaultdict(list)
    for member, number, name in rows:
        pursuits[member].append(schemas.PursuitSummary(number=number, name=name))
    return dict(pursuits)


def issue_pursuits(session: Session, issues: Collection[models.Issue]) -> dict[int, list[schemas.PursuitSummary]]:
    """Each issue's pursuits by its number: its initiative's. Each issue's `initiative` is read, so load it with the issue."""
    initiative_of = {issue.number: issue.initiative.number for issue in issues if issue.initiative is not None}
    held = pursuits_of(session, set(initiative_of.values()))
    return {issue.number: held.get(initiative_of[issue.number], []) if issue.number in initiative_of else [] for issue in issues}


def filtered_pursuit(session: Session, ref: int | str) -> models.Pursuit:
    """The pursuit a filter names by number or exact name, refused as a write's would be."""
    pursuit = find_pursuit(session, ref)
    if pursuit is None:
        raise unknown_pursuits(session, [ref])
    return pursuit


def in_pursuit(session: Session, kind: Kind, ref: int | str) -> ColumnElement[bool]:
    """Whether a record of `kind` is in the pursuit: an idea or an initiative itself, an issue through its initiative."""
    pursuit = filtered_pursuit(session, ref)
    members = select(models.RecordPursuit.record_number).where(models.RecordPursuit.pursuit_id == pursuit.id)
    if kind == Kind.ISSUE:
        return models.Issue.initiative_id.in_(select(models.Initiative.id).where(models.Initiative.number.in_(members)))
    return models.KIND_MODELS[kind].number.in_(members)


def narrowed_to_pursuit(query: Select, kind: Kind, ref: int | str | None, session: Session) -> Select:
    return query if ref is None else query.where(in_pursuit(session, kind, ref))


def members_of(session: Session, pursuit_ids: Collection[UUID]) -> dict[UUID, list[schemas.RecordSummary]]:
    """Each pursuit's ideas and initiatives by its id, in number order, whatever their status."""
    if not pursuit_ids:
        return {}
    rows = session.execute(
        select(models.RecordPursuit.pursuit_id, models.RecordPursuit.record_number)
        .where(models.RecordPursuit.pursuit_id.in_(list(pursuit_ids)))
        .order_by(models.RecordPursuit.record_number)
    ).all()
    summaries = record_summaries(session, [number for _, number in rows])
    members: dict[UUID, list[schemas.RecordSummary]] = defaultdict(list)
    for pursuit_id, number in rows:
        members[pursuit_id].append(summaries[number])
    return dict(members)
