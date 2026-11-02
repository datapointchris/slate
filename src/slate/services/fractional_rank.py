"""Order every record by a float `rank`, the way Linear's `sortOrder` does.

A new record goes one past the last, placed by the `records_rank_at_end` trigger. A move takes the
midpoint of its two new neighbors, so it writes one row. Halving a gap runs out of precision
eventually, so when the gap left between two neighbors falls below `MIN_GAP`, every rank is
renumbered to whole numbers in its current order and the midpoint is taken again.

Ideas, initiatives and issues share the one space, even where the queue groups them. A record
placed after its neighbor A lands between A and whatever ranks next among every record, and nothing
in A's group can sit between those two.
"""

from sqlalchemy import func
from sqlalchemy import select
from sqlalchemy import update
from sqlalchemy.orm import Session

from slate.models import Record

# Two ranks this far apart still have a double between them at any rank below
# 10^9, and a rank grows by one each time a record is placed last. A renumber
# leaves every gap at 1, far above this.
MIN_GAP = 1e-6


def renumber_ranks(session: Session) -> None:
    """Rewrite every rank as its position in the current order, starting at 1.

    Pending changes are flushed first, because the rows are expired afterwards
    so every loaded row reads its new rank.
    """
    session.flush()
    position = func.row_number().over(order_by=(Record.rank, Record.number)).label('position')
    ordered = select(Record.number, position).subquery()
    session.execute(
        update(Record).where(Record.number == ordered.c.number).values(rank=ordered.c.position),
        execution_options={'synchronize_session': False},
    )
    session.expire_all()


def neighbor_before(session: Session, row: Record, anchor: Record) -> float | None:
    """The rank of whatever sits directly before `anchor`, other than `row` itself."""
    query = select(Record.rank).where(Record.rank < anchor.rank, Record.number != row.number)
    return session.scalar(query.order_by(Record.rank.desc()).limit(1))


def neighbor_after(session: Session, row: Record, anchor: Record) -> float | None:
    query = select(Record.rank).where(Record.rank > anchor.rank, Record.number != row.number)
    return session.scalar(query.order_by(Record.rank.asc()).limit(1))


def between(low: float | None, high: float | None) -> float | None:
    """The midpoint, or one step past an open end. None when the gap is spent."""
    if low is not None and high is not None:
        return (low + high) / 2 if high - low >= MIN_GAP else None
    if low is not None:
        return low + 1.0
    if high is not None:
        return high - 1.0
    return 1.0


def rank_beside(session: Session, row: Record, before: Record | None, after: Record | None) -> float | None:
    if before is not None:
        return between(neighbor_before(session, row, before), before.rank)
    if after is not None:
        return between(after.rank, neighbor_after(session, row, after))
    raise ValueError('a move needs a neighbor: before or after')


def move_record(session: Session, row: Record, *, before: Record | None = None, after: Record | None = None) -> None:
    """Rank `row` immediately before `before`, or immediately after `after`."""
    rank = rank_beside(session, row, before, after)
    if rank is None:
        renumber_ranks(session)
        rank = rank_beside(session, row, before, after)
    if rank is None:
        raise RuntimeError('rank gap still spent after renumbering')
    row.rank = rank
