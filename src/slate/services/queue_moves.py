"""Move a record in the queue `slate next` reads, refusing a move that would not land where it asks.

A slot moves among the slots of its priority: an idea, an initiative with its issues, or an issue
outside any initiative. Placed beside an issue inside an initiative, it goes beside that whole
initiative. An issue inside an initiative moves only among that initiative's issues. An issue sorting
just ahead of what waits on it moves with that, so its only neighbors are the issues sorting there too.
"""

from __future__ import annotations

from dataclasses import dataclass

from fastapi import HTTPException
from fastapi import status
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.record import PRIORITIES
from slate.models.record import Kind
from slate.services.fractional_rank import move_record
from slate.services.issue_readiness import IssueReadiness
from slate.services.issue_readiness import QueueKey
from slate.services.issue_readiness import measure_readiness
from slate.services.issue_readiness import priority_of
from slate.services.issue_readiness import urgency
from slate.services.issue_views import calendar_now
from slate.services.record_refs import resolve_record


@dataclass(frozen=True)
class Standing:
    """Where a record sits: the slot holding it, that slot's urgency, and for an unclosed issue its place.

    `inside` names the initiative an issue sits inside. `raised_by` is the issue whose wait puts it
    ahead of its own place.
    """

    record: models.Record
    slot: int
    urgency: int
    key: QueueKey | None = None
    inside: str | None = None
    raised_by: int | None = None


def standing(session: Session, readiness: IssueReadiness, record: models.Record) -> Standing:
    match record.kind:
        case Kind.ISSUE:
            issue = session.scalars(select(models.Issue).where(models.Issue.number == record.number)).one()
            key = readiness.queue_key.get(issue.id)
            if key is None:
                return Standing(record, record.number, urgency(issue.priority))
            slot = readiness.slot_of[issue.id]
            inside = issue.initiative.name if slot != record.number and issue.initiative is not None else None
            return Standing(record, slot, readiness.home_key[issue.id].urgency, key, inside, readiness.raised_by.get(issue.id))
        case Kind.INITIATIVE:
            initiative = session.scalars(select(models.Initiative).where(models.Initiative.number == record.number)).one()
            return Standing(record, record.number, urgency(initiative.priority))
        case Kind.IDEA:
            idea = session.scalars(select(models.Idea).where(models.Idea.number == record.number)).one()
            return Standing(record, record.number, urgency(idea.priority))


def conflict(detail: str) -> HTTPException:
    return HTTPException(status_code=status.HTTP_409_CONFLICT, detail=detail)


def priority_phrase(urgency_value: int) -> str:
    priority = priority_of(urgency_value)
    return 'no priority' if priority == 0 else f'{PRIORITIES[priority]} priority'


def placement(session: Session, mover: Standing, beside: Standing) -> models.Record:
    """The record to rank `mover` beside, so it lands directly beside `beside` in the queue."""
    m, a = mover.record.number, beside.record.number
    if mover.raised_by is not None:
        if mover.key is not None and beside.key is not None and beside.key[:6] == mover.key[:6]:
            return beside.record
        raise conflict(
            f'#{m} sorts just ahead of #{mover.raised_by}, which waits on it, so it moves with #{mover.raised_by}. '
            f'It is placed only beside another issue sorting there.'
        )
    if beside.raised_by is not None:
        raise conflict(
            f'#{a} sorts just ahead of #{beside.raised_by}, which waits on it, so nothing is placed beside it. '
            f'Place #{m} beside #{beside.raised_by} instead.'
        )
    if mover.inside is not None:
        if beside.slot == mover.slot and a != mover.slot:
            return beside.record
        raise conflict(
            f"#{m} sits inside initiative #{mover.slot} {mover.inside!r}, so it moves only among that initiative's issues. "
            f"Take it out of the initiative, or give it a priority of its own above the initiative's, to place it elsewhere."
        )
    if beside.slot == m:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail=f'#{a} sits inside #{m}, so #{m} cannot be placed beside it.'
        )
    if beside.urgency != mover.urgency:
        raise conflict(
            f'#{m} sorts at {priority_phrase(mover.urgency)} and #{a} at {priority_phrase(beside.urgency)}. '
            f'A move orders records only within one priority. Changing the priority of #{m} is what moves it past #{a}.'
        )
    return session.scalars(select(models.Record).where(models.Record.number == beside.slot)).one()


def move_in_queue(session: Session, zone: str, record: models.Record, move: schemas.RecordRankMove) -> None:
    """Rank `record` directly before or after the record `move` names, of any kind."""
    before = move.before is not None
    anchor_ref = move.before if move.before is not None else move.after
    assert anchor_ref is not None, 'RecordRankMove holds exactly one neighbor'
    anchor = resolve_record(session, anchor_ref)
    if anchor.number == record.number:
        raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail='A record cannot be placed beside itself')
    readiness = measure_readiness(session, *calendar_now(zone))
    target = placement(session, standing(session, readiness, record), standing(session, readiness, anchor))
    if before:
        move_record(session, record, before=target)
    else:
        move_record(session, record, after=target)
