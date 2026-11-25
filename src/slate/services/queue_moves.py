"""Move a record in the queue `slate next` reads, refusing a move that would not land where it asks.

A slot moves among the slots of its priority: an idea, an initiative with its issues, or an issue
outside any initiative. Placed beside an issue inside an initiative, it goes beside that whole
initiative. An issue inside an initiative moves only among that initiative's issues. An issue sorting
just ahead of what waits on it moves with that, so its only neighbors are the issues sorting there too.
A slot never moves across the pin, which only pinning or unpinning crosses.

`board_moves` answers, for each row of the queue board, where the rank write would take it, so a
board offers the move this module would make rather than a copy of its rules.
"""

from __future__ import annotations

from collections.abc import Callable
from collections.abc import Sequence
from dataclasses import dataclass
from uuid import UUID

from fastapi import HTTPException
from fastapi import status
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.record import PRIORITIES
from slate.models.record import Kind
from slate.services.fractional_rank import move_record
from slate.services.issue_readiness import PINNED
from slate.services.issue_readiness import IssueReadiness
from slate.services.issue_readiness import QueueKey
from slate.services.issue_readiness import measure_readiness
from slate.services.issue_readiness import priority_of
from slate.services.issue_views import calendar_now
from slate.services.record_lookup import resolve_record


@dataclass(frozen=True)
class Standing:
    """Where a record sits: the slot holding it, that slot's urgency, and for an unclosed issue its place.

    `inside` is the name of the initiative holding an issue. `raised_by` is the issue whose wait puts it
    ahead of its own place. `pin` is the pin term of the slot holding it.
    """

    number: int
    slot: int
    urgency: int
    pin: int
    key: QueueKey | None = None
    inside: str | None = None
    raised_by: int | None = None


def standing(session: Session, readiness: IssueReadiness, record: models.Record) -> Standing:
    """Where `record` sits, read from `readiness`, which also cuts the queue. A record with no place in the queue is refused."""
    match record.kind:
        case Kind.ISSUE:
            issue = session.scalars(select(models.Issue).where(models.Issue.number == record.number)).one()
            found = issue_standing(readiness, issue.id, record.number, None if issue.initiative is None else issue.initiative.name)
            if found is None:
                raise outside_the_queue(record, f'a {issue.status} issue')
            return found
        case Kind.INITIATIVE:
            if (key := readiness.slot_key_by_number.get(record.number)) is None:
                initiative = session.scalars(select(models.Initiative).where(models.Initiative.number == record.number)).one()
                raise outside_the_queue(record, f'a {initiative.status} initiative')
            return slot_standing(record.number, key)
        case Kind.IDEA:
            # Every idea is open, so the queue holds a place for each.
            return slot_standing(record.number, readiness.slot_key_by_number[record.number])
        case Kind.PURSUIT:
            raise outside_the_queue(record, 'a pursuit, which holds no work of its own')


def issue_standing(readiness: IssueReadiness, issue_id: UUID, number: int, initiative_name: str | None) -> Standing | None:
    """Where an issue sits, or None where the queue holds no place for it. `initiative_name` names its initiative, if it has one."""
    if (key := readiness.queue_key.get(issue_id)) is None:
        return None
    slot = readiness.slot_of[issue_id]
    home = readiness.home_key[issue_id]
    inside = initiative_name if slot != number else None
    return Standing(number, slot, home.urgency, home.pin, key, inside, readiness.raised_by.get(issue_id))


def slot_standing(number: int, key: QueueKey) -> Standing:
    """Where a record standing as its own slot sits, at `key`: an idea, an initiative, or an issue outside one."""
    return Standing(number, number, key.urgency, key.pin, key)


def conflict(detail: str) -> HTTPException:
    return HTTPException(status_code=status.HTTP_409_CONFLICT, detail=detail)


def outside_the_queue(record: models.Record, what: str) -> HTTPException:
    return conflict(f'#{record.number} is {what}, so the queue holds no place for it to move to or beside.')


def priority_phrase(urgency_value: int) -> str:
    priority = priority_of(urgency_value)
    return 'no priority' if priority == 0 else f'{PRIORITIES[priority]} priority'


def placement(mover: Standing, beside: Standing) -> int:
    """The number of the record to rank `mover` beside, so it lands directly beside `beside` in the queue."""
    m, a = mover.number, beside.number
    if mover.raised_by is not None:
        if mover.key is not None and beside.key is not None and beside.key.place_before_ties == mover.key.place_before_ties:
            return a
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
            return a
        raise conflict(
            f"#{m} sits inside initiative #{mover.slot} {mover.inside!r}, so it moves only among that initiative's issues. "
            f"Take it out of the initiative, or give it a priority of its own above the initiative's, to place it elsewhere."
        )
    if beside.slot == m:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail=f'#{a} sits inside #{m}, so #{m} cannot be placed beside it.'
        )
    if beside.pin != mover.pin:
        pinned, unpinned = (m, a) if mover.pin == PINNED else (a, m)
        raise conflict(
            f'#{pinned} is pinned and #{unpinned} is not, so a move cannot place one beside the other. '
            f'Pinning or unpinning #{m} is what moves it past #{a}.'
        )
    if beside.urgency != mover.urgency:
        raise conflict(
            f'#{m} sorts at {priority_phrase(mover.urgency)} and #{a} at {priority_phrase(beside.urgency)}. '
            f'A move orders records only within one priority. Changing the priority of #{m} is what moves it past #{a}.'
        )
    return beside.slot


def move_in_queue(session: Session, zone: str, record: models.Record, move: schemas.RecordRankMove) -> None:
    """Rank `record` directly before or after the record `move` names, of any kind."""
    before = move.before is not None
    anchor_ref = move.before if move.before is not None else move.after
    assert anchor_ref is not None, 'RecordRankMove holds exactly one neighbor'
    anchor = resolve_record(session, anchor_ref)
    if anchor.number == record.number:
        raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail='A record cannot be placed beside itself')
    readiness = measure_readiness(session, *calendar_now(zone))
    number = placement(standing(session, readiness, record), standing(session, readiness, anchor))
    target = session.scalars(select(models.Record).where(models.Record.number == number)).one()
    if before:
        move_record(session, record, before=target)
    else:
        move_record(session, record, after=target)


def board_moves(queue: Sequence[Standing], waiter: Callable[[int], Standing]) -> list[schemas.QueueRowMoves]:
    """Where a move ahead and a move behind take each row of `queue`, which is in the queue's order.

    `waiter(number)` is where issue `number` sits. A raised row waits on such an issue, and the
    queue may hold no row for it.
    """
    return [
        schemas.QueueRowMoves(ahead=board_move(queue, at, -1, waiter), behind=board_move(queue, at, 1, waiter)) for at in range(len(queue))
    ]


def board_move(queue: Sequence[Standing], at: int, step: int, waiter: Callable[[int], Standing]) -> schemas.QueueMove:
    """Where the row at `at` moves past the first row beyond what it carries, `step` -1 for ahead and 1 for behind.

    Nothing is placed beside a raised row, so a move reaching one is placed beside the issue it
    waits on, as the refusal of such a move says to.
    """
    mover = queue[at]
    edge = at
    while 0 <= edge + step < len(queue) and carries(mover, queue[edge + step]):
        edge += step
    reached = edge + step
    if not 0 <= reached < len(queue):
        where, side = ('first', 'ahead of') if step < 0 else ('last', 'behind')
        return schemas.QueueMove(refused=f'#{mover.number} is {where} in the queue, so no row sits {side} it.')
    beside = queue[reached]
    while mover.raised_by is None and beside.raised_by is not None:
        beside = waiter(beside.raised_by)
    try:
        number = placement(mover, beside)
    except HTTPException as refusal:
        return schemas.QueueMove(refused=refusal.detail)
    passes = []
    while 0 <= reached < len(queue) and lands_beside(number, beside, queue[reached]):
        passes.append(queue[reached].number)
        reached += step
    return schemas.QueueMove(beside=number, passes=passes)


def carries(record: Standing, row: Standing) -> bool:
    """Whether `row` sorts as part of `record`'s place, so a move of `record` takes it along.

    A slot takes every row sorting in it. An issue inside an initiative takes the rows sorting just
    ahead of it. A raised row takes only itself, because the rows it raised sort by their depth.
    """
    assert record.key is not None and row.key is not None, 'every queued row has a place'
    if record.raised_by is not None:
        return row.number == record.number
    if record.slot == record.number:
        return row.key.slot_number == record.number
    return row.key[:6] == record.key[:6]


def lands_beside(number: int, beside: Standing, row: Standing) -> bool:
    """Whether a move ranked beside `number` passes `row`, because `row` sorts in `number`'s place."""
    assert row.key is not None, 'every queued row has a place'
    if number == beside.number:
        return carries(beside, row)
    return row.key.slot_number == number
