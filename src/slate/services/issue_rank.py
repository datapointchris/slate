"""Place issues in the one global order that breaks ties inside a priority.

`services/fractional_rank.py` does the placing. What is particular to issues is
that the neighbors are read across every priority, which is why the `rank`
endpoint refuses an anchor of another effective priority.
"""

from sqlalchemy.orm import Session

from slate import models
from slate.services import fractional_rank


def rank_at_end(session: Session) -> float:
    return fractional_rank.rank_at_end(session, models.Issue)


def move_issue(session: Session, issue: models.Issue, *, before: models.Issue | None = None, after: models.Issue | None = None) -> None:
    """Rank `issue` immediately before `before`, or immediately after `after`.

    Given an anchor of another effective priority, the issue lands wherever that
    priority's ranks fall among its own.
    """
    fractional_rank.move_ranked(session, issue, before=before, after=after)
