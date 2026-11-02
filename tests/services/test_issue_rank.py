"""`move_issue` reads its neighbors from the table, so these run against a real one."""

import pytest
from sqlalchemy import select

from slate import models
from slate.services.fractional_rank import MIN_GAP
from slate.services.issue_rank import move_issue


@pytest.fixture
def session(txn_api):
    _, session = txn_api
    return session


def place(session, *ranks: float) -> list[models.Issue]:
    issues = [models.Issue(title=f'ranked {rank}', rank=rank) for rank in ranks]
    session.add_all(issues)
    session.flush()
    return issues


def order(session) -> list[str]:
    return list(session.scalars(select(models.Issue.title).order_by(models.Issue.rank)).all())


def test_a_spent_gap_renumbers_every_rank_and_keeps_the_order(session):
    tight = 1.0 + MIN_GAP / 4
    first, second, mover = place(session, 1.0, tight, 5.0)
    move_issue(session, mover, after=first)
    session.flush()

    assert order(session) == ['ranked 1.0', 'ranked 5.0', f'ranked {tight}']
    assert (first.rank, mover.rank, second.rank) == (1.0, 1.5, 2.0)
