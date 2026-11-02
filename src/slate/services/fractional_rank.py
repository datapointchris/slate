"""Order a table's rows by a float `rank`, the way Linear's `sortOrder` does.

A new row goes one past the last. A move takes the midpoint of its two new
neighbors, so it writes one row. Halving a gap runs out of precision eventually,
so when the gap left between two neighbors falls below `MIN_GAP`, every rank in
the table is renumbered to whole numbers in its current order and the midpoint
is taken again.

One rank space covers the whole table, even where rows are shown in groups. A
row placed after its neighbor A lands between A and whatever ranks next in the
table, and nothing in A's group can sit between those two.
"""

from sqlalchemy import func
from sqlalchemy import select
from sqlalchemy import update
from sqlalchemy.orm import Session

from slate.database.fractionally_ranked import FractionallyRanked

# Two ranks this far apart still have a double between them at any rank below
# 10^9, and a rank grows by one each time a row is placed last. A renumber
# leaves every gap at 1, far above this.
MIN_GAP = 1e-6


def rank_at_end(session: Session, model: type[FractionallyRanked]) -> float:
    highest = session.scalar(select(func.max(model.rank)))
    return (highest or 0.0) + 1.0


def renumber_ranks(session: Session, model: type[FractionallyRanked]) -> None:
    """Rewrite every rank in the table as its position in the current order, starting at 1.

    Pending changes are flushed first, because the rows are expired afterwards
    so every loaded row reads its new rank.
    """
    session.flush()
    position = func.row_number().over(order_by=(model.rank, model.created_ts, model.id)).label('position')
    ordered = select(model.id, position).subquery()
    session.execute(
        update(model).where(model.id == ordered.c.id).values(rank=ordered.c.position),
        execution_options={'synchronize_session': False},
    )
    session.expire_all()


def neighbor_before(session: Session, row: FractionallyRanked, anchor: FractionallyRanked) -> float | None:
    """The rank of whatever sits directly before `anchor`, other than `row` itself."""
    model = type(row)
    return session.scalar(select(model.rank).where(model.rank < anchor.rank, model.id != row.id).order_by(model.rank.desc()).limit(1))


def neighbor_after(session: Session, row: FractionallyRanked, anchor: FractionallyRanked) -> float | None:
    model = type(row)
    return session.scalar(select(model.rank).where(model.rank > anchor.rank, model.id != row.id).order_by(model.rank.asc()).limit(1))


def between(low: float | None, high: float | None) -> float | None:
    """The midpoint, or one step past an open end. None when the gap is spent."""
    if low is not None and high is not None:
        return (low + high) / 2 if high - low >= MIN_GAP else None
    if low is not None:
        return low + 1.0
    if high is not None:
        return high - 1.0
    return 1.0


def rank_beside[R: FractionallyRanked](session: Session, row: R, before: R | None, after: R | None) -> float | None:
    if before is not None:
        return between(neighbor_before(session, row, before), before.rank)
    if after is not None:
        return between(after.rank, neighbor_after(session, row, after))
    raise ValueError('a move needs a neighbor: before or after')


def move_ranked[R: FractionallyRanked](session: Session, row: R, *, before: R | None = None, after: R | None = None) -> None:
    """Rank `row` immediately before `before`, or immediately after `after`."""
    rank = rank_beside(session, row, before, after)
    if rank is None:
        renumber_ranks(session, type(row))
        rank = rank_beside(session, row, before, after)
    if rank is None:
        raise RuntimeError('rank gap still spent after renumbering')
    row.rank = rank
