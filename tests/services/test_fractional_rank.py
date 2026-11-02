"""`move_record` reads its neighbors from the table, so these run against a real one."""

import pytest
from sqlalchemy import select

from slate import models
from slate.services.fractional_rank import MIN_GAP
from slate.services.fractional_rank import move_record


@pytest.fixture
def session(txn_api):
    _, session = txn_api
    return session


def place(session, *ranks: float) -> list[models.Record]:
    ideas = [models.Idea(title=f'ranked {rank}', rank=rank) for rank in ranks]
    session.add_all(ideas)
    session.flush()
    return [idea.record for idea in ideas]


def order(session) -> list[str]:
    return list(session.scalars(select(models.Idea.title).join(models.Record).order_by(models.Record.rank)).all())


def test_a_spent_gap_renumbers_every_rank_and_keeps_the_order(session):
    tight = 1.0 + MIN_GAP / 4
    first, second, mover = place(session, 1.0, tight, 5.0)
    move_record(session, mover, after=first)
    session.flush()

    assert order(session) == ['ranked 1.0', 'ranked 5.0', f'ranked {tight}']
    assert (first.rank, mover.rank, second.rank) == (1.0, 1.5, 2.0)
