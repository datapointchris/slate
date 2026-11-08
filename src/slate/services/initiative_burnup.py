"""An initiative's burn-up: how many issues it held and how many of those were completed, at each moment either changed.

An issue joins an initiative when a write sets its `initiative_id` to it, and leaves when a write
moves it elsewhere. The audit trail holds each of those writes, so membership is read back from
there rather than stored twice. An issue holding no write that joined it was in the initiative
from its creation, as an issue imported without its history was.

Closes are read from each issue as it stands. A canceled issue leaves the scope when it closed,
and a completed one counts from its close, or from when it joined where that came later. A close
at no recorded time counts from when the issue joined. A deleted issue is in no step.
"""

from __future__ import annotations

import datetime as dt
from collections import defaultdict
from collections.abc import Sequence
from dataclasses import dataclass
from itertools import groupby
from operator import itemgetter
from uuid import UUID

from sqlalchemy import Text
from sqlalchemy import cast
from sqlalchemy import or_
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas

Change = models.RecordChange

# One change to the steps: when, then what it adds to the scope and to the completed.
type StepChange = tuple[dt.datetime, int, int]


@dataclass(frozen=True)
class Move:
    """One write to an issue's `initiative_id`, naming the initiative it left and the one it joined, each by its key."""

    at: dt.datetime
    left: str | None
    joined: str | None


def burnups(
    session: Session, initiatives: Sequence[models.Initiative], members: Sequence[models.Issue], now: dt.datetime
) -> dict[UUID, list[schemas.BurnupStep]]:
    """Each initiative's steps, read once for all of them. `members` is every issue in any of them now."""
    keys = [str(initiative.id) for initiative in initiatives]
    initiative_id = Change.changes['initiative_id']
    rows = session.execute(
        select(Change.record_id, Change.changed_ts, initiative_id['old'].astext, initiative_id['new'].astext)
        .where(
            Change.table_name == models.Issue.__tablename__,
            or_(initiative_id['old'].astext.in_(keys), initiative_id['new'].astext.in_(keys)),
        )
        .order_by(Change.changed_ts, Change.id)
    ).all()
    moves: dict[str, list[Move]] = defaultdict(list)
    for record_id, at, left, joined in rows:
        moves[record_id].append(Move(at, left, joined))

    issues = {str(issue.id): issue for issue in members}
    if former := [key for key in moves if key not in issues]:
        issues |= {str(issue.id): issue for issue in session.scalars(select(models.Issue).where(cast(models.Issue.id, Text).in_(former)))}

    changes: dict[str, list[StepChange]] = defaultdict(list)
    for key, issue in issues.items():
        touched = {str(issue.initiative_id)} | {move.left for move in moves[key]} | {move.joined for move in moves[key]}
        for initiative in set(keys).intersection(touched):
            changes[initiative] += step_changes(issue, spans_in(issue, initiative, moves[key]))
    return {initiative.id: steps(changes[str(initiative.id)], now) for initiative in initiatives}


def spans_in(issue: models.Issue, initiative: str, moves: Sequence[Move]) -> list[tuple[dt.datetime, dt.datetime | None]]:
    """When `issue` joined and left the initiative keyed `initiative`, each time, the last left open where it is in it still."""
    spans: list[tuple[dt.datetime, dt.datetime | None]] = []
    joined: dt.datetime | None = None
    for move in moves:
        if move.joined == initiative:
            joined = move.at
        elif move.left == initiative:
            spans.append((joined or issue.created_ts, move.at))
            joined = None
    if str(issue.initiative_id) == initiative:
        spans.append((joined or issue.created_ts, None))
    return spans


def step_changes(issue: models.Issue, spans: Sequence[tuple[dt.datetime, dt.datetime | None]]) -> list[StepChange]:
    changes: list[StepChange] = []
    for joined, left in spans:
        closed = max(issue.closed_ts or joined, joined)
        if issue.status == 'canceled':
            if closed == joined:
                continue
            left = closed if left is None else min(left, closed)
        changes.append((joined, 1, 0))
        if left is not None:
            changes.append((left, -1, 0))
        if issue.status == 'completed' and (left is None or closed < left):
            changes.append((closed, 0, 1))
            if left is not None:
                changes.append((left, 0, -1))
    return changes


def steps(changes: list[StepChange], now: dt.datetime) -> list[schemas.BurnupStep]:
    out: list[schemas.BurnupStep] = []
    scope = completed = 0
    for at, together in groupby(sorted(changes, key=itemgetter(0)), key=itemgetter(0)):
        for _, scoped, done in together:
            scope, completed = scope + scoped, completed + done
        out.append(schemas.BurnupStep(ts=at, scope=scope, completed=completed))
    if out and out[-1].ts < now:
        out.append(schemas.BurnupStep(ts=now, scope=scope, completed=completed))
    return out
