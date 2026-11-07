"""Which issues a live claim on an initiative holds, which is its plan.

The queue, `POST /claims/next` and a hold's warnings each read this, so they agree on one issue.
"""

from __future__ import annotations

from collections.abc import Mapping

from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate.services.record_work import read_work_graph
from slate.services.record_work import record_work


def held_plans(session: Session, held: Mapping[int, models.Claim]) -> dict[int, models.Claim]:
    """Each issue in the plan of an initiative under a live claim, with that claim. The earlier claim keeps an issue two plans share."""
    numbers = session.scalars(select(models.Initiative.number).where(models.Initiative.number.in_(list(held)))).all()
    if not numbers:
        return {}
    graph = read_work_graph(session)
    plans: dict[int, models.Claim] = {}
    for claim in sorted((held[number] for number in numbers), key=lambda claim: claim.started_ts):
        for number in record_work(session, claim.record_number, graph).unblocks:
            plans.setdefault(number, claim)
    return plans
