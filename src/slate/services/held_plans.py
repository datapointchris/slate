"""Which issues a live claim on an initiative holds, which is its plan, and which claim holds a record.

The queue, the due order, `POST /claims/next` and a hold's warnings each read this, so they agree on one issue.
"""

from __future__ import annotations

from collections.abc import Mapping

from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate.models.worklog import BUILD
from slate.services.record_work import initiative_work
from slate.services.record_work import read_work_graph


def held_plans(session: Session, held: Mapping[int, models.Claim]) -> dict[int, models.Claim]:
    """Each issue in the plan of an initiative under a live claim, with that claim. The earlier claim keeps an issue two plans share.

    Its queries stay the same however many initiatives are held, because a board polls it.
    """
    initiatives = dict(
        session.execute(select(models.Initiative.number, models.Initiative.id).where(models.Initiative.number.in_(list(held)))).all()
    )
    if not initiatives:
        return {}
    graph = read_work_graph(session)
    plans: dict[int, models.Claim] = {}
    for claim in sorted((held[number] for number in initiatives), key=lambda claim: claim.started_ts):
        for number in initiative_work(graph, claim.record_number, initiatives[claim.record_number]).unblocks:
            plans.setdefault(number, claim)
    return plans


def holding_claim(
    number: int, step: str | None, held: Mapping[int, models.Claim], plans: Mapping[int, models.Claim]
) -> models.Claim | None:
    """The live claim holding a record: its own, else the claimed initiative's whose plan holds it at `build`.

    A decision waits on a person whoever coordinates, so a plan never holds one. `step` is the step
    the record's queue row asks for, or None where it has none.
    """
    claim = held.get(number)
    if claim is None and step == BUILD:
        claim = plans.get(number)
    return claim
