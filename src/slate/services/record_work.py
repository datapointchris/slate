"""A record's own open work and everything it transitively waits on, whatever initiative or repo holds it.

A coordinator taking a plan to done needs this one read. It is the transitive closure of the graph
from the record's issues, as `bazel query 'deps(//target)'` reads a build graph. An issue waits on
each issue it depends on and each child, since a parent is finished by its children. While its
initiative waits on an open record, it also waits on that issue, or on each open issue of that
initiative, since an initiative is finished by its issues. A closed issue waits on nothing and
holds nothing up, so the walk neither enters nor crosses one.
"""

from __future__ import annotations

from collections import defaultdict
from collections import deque
from dataclasses import dataclass
from typing import Any
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate.models.issue import CLOSED_ISSUE_STATUSES
from slate.models.record import Kind
from slate.services.initiative_waits import holding_waits
from slate.services.record_refs import resolve_record


@dataclass(frozen=True)
class RecordWork:
    """`unblocks` holds each open issue in the closure, with the numbers in it that wait on that issue directly."""

    number: int
    kind: Kind
    unblocks: dict[int, list[int]]


@dataclass(frozen=True)
class WorkGraph:
    """Every open issue by id, and the issues each one waits on. A read walking several records loads it once."""

    by_id: dict[UUID, Any]
    waits_on: dict[UUID, list[UUID]]


def read_work_graph(session: Session) -> WorkGraph:
    rows = session.execute(
        select(models.Issue.id, models.Issue.number, models.Issue.status, models.Issue.parent_id, models.Issue.initiative_id)
    ).all()
    by_id = {row.id: row for row in rows if row.status not in CLOSED_ISSUE_STATUSES}

    waits_on: dict[UUID, list[UUID]] = {}
    for issue_id, depends_on_id in session.execute(select(models.IssueDependency.issue_id, models.IssueDependency.depends_on_id)).all():
        waits_on.setdefault(issue_id, []).append(depends_on_id)
    open_issues_of: dict[UUID, list[UUID]] = defaultdict(list)
    for row in by_id.values():
        if row.parent_id is not None:
            waits_on.setdefault(row.parent_id, []).append(row.id)
        if row.initiative_id is not None:
            open_issues_of[row.initiative_id].append(row.id)
    open_by_number = {row.number: row.id for row in by_id.values()}
    for wait in holding_waits(session):
        if wait.depends_on_initiative_id is not None:
            waited_on = open_issues_of.get(wait.depends_on_initiative_id, [])
        else:
            waited_on = [open_by_number[wait.depends_on_number]] if wait.depends_on_number in open_by_number else []
        for waiter in open_issues_of.get(wait.initiative_id, []):
            waits_on.setdefault(waiter, []).extend(waited_on)
    return WorkGraph(by_id=by_id, waits_on=waits_on)


def record_work(session: Session, number: int, graph: WorkGraph | None = None) -> RecordWork:
    record = resolve_record(session, number)
    kind = Kind(record.kind)
    work_graph = graph or read_work_graph(session)
    by_id, waits_on = work_graph.by_id, work_graph.waits_on

    if kind == Kind.INITIATIVE:
        initiative_id = session.scalar(select(models.Initiative.id).where(models.Initiative.number == number))
        roots = [row.id for row in by_id.values() if row.initiative_id == initiative_id]
    else:
        roots = [row.id for row in by_id.values() if row.number == number]

    unblocks: dict[int, list[int]] = {by_id[root].number: [] for root in roots}
    walk = deque(roots)
    while walk:
        waiter = walk.popleft()
        for waited_on in waits_on.get(waiter, []):
            if waited_on not in by_id:
                continue
            target = by_id[waited_on].number
            if target not in unblocks:
                unblocks[target] = []
                walk.append(waited_on)
            unblocks[target].append(by_id[waiter].number)
    return RecordWork(number=number, kind=kind, unblocks={issue: sorted(set(waiters)) for issue, waiters in unblocks.items()})
