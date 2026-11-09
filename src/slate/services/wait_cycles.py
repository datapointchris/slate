"""Refuse a write that would leave a record waiting on itself.

A record waits on another along four kinds of edge, each read by record number:

- an issue on each issue it depends on
- a parent on each of its children, since a parent is finished by its children
- an initiative on each record it waits on, and on each of its unclosed issues, since it is
  finished by them
- an unclosed issue on each record its initiative waits on

A loop through any mix of them is a deadlock, because nothing in it can start. Every loop is
refused on write, so a write is checked by applying it to the graph as it stands and searching
from each record the new edges touch for a way back to itself. The refusal prints the loop, so
the caller sees which edge to cut.
"""

from __future__ import annotations

from collections import defaultdict
from collections import deque
from collections.abc import Iterator
from dataclasses import dataclass
from dataclasses import field

from fastapi import HTTPException
from fastapi import status
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate.models.issue import CLOSED_ISSUE_STATUSES

# A step from an issue to what its initiative waits on goes through that initiative.
Step = tuple[int, int | None]


@dataclass
class WaitGraph:
    """Every wait edge as it stands, by record number. The setters apply a write before it is checked."""

    initiatives: set[int] = field(default_factory=set)
    unclosed: set[int] = field(default_factory=set)
    depends_on: dict[int, set[int]] = field(default_factory=lambda: defaultdict(set))
    parent_of: dict[int, int] = field(default_factory=dict)
    initiative_of: dict[int, int] = field(default_factory=dict)
    waits_on: dict[int, set[int]] = field(default_factory=lambda: defaultdict(set))

    def add_dependency(self, issue: int, depends_on: int) -> None:
        self.depends_on[issue].add(depends_on)

    def add_wait(self, initiative: int, record: int) -> None:
        self.waits_on[initiative].add(record)

    def set_parent(self, issue: int, parent: int | None) -> None:
        self.parent_of.pop(issue, None)
        if parent is not None:
            self.parent_of[issue] = parent

    def set_initiative(self, issue: int, initiative: int | None) -> None:
        self.initiative_of.pop(issue, None)
        if initiative is not None:
            self.initiative_of[issue] = initiative

    def loop_through(self, start: int) -> list[int] | None:
        """The records of a loop from `start` back to itself, each initiative an issue waits through included, or None."""
        children: dict[int, list[int]] = defaultdict(list)
        for child, parent in self.parent_of.items():
            children[parent].append(child)
        members: dict[int, list[int]] = defaultdict(list)
        for issue, initiative in self.initiative_of.items():
            if issue in self.unclosed:
                members[initiative].append(issue)

        def following(number: int) -> Iterator[Step]:
            if number in self.initiatives:
                yield from ((record, None) for record in sorted(self.waits_on.get(number, ())))
                yield from ((issue, None) for issue in sorted(members.get(number, ())))
                return
            yield from ((issue, None) for issue in sorted(self.depends_on.get(number, ())))
            yield from ((child, None) for child in sorted(children.get(number, ())))
            initiative = self.initiative_of.get(number)
            if number in self.unclosed and initiative is not None:
                yield from ((record, initiative) for record in sorted(self.waits_on.get(initiative, ())))

        came_from: dict[int, Step] = {}

        def path_to(number: int) -> list[int]:
            path = [number]
            while number != start:
                previous, through = came_from[number]
                path.extend(([] if through is None else [through]) + [previous])
                number = previous
            return path[::-1]

        walk = deque([start])
        while walk:
            current = walk.popleft()
            for number, through in following(current):
                if number == start:
                    return [*path_to(current), *([] if through is None else [through]), start]
                if number not in came_from:
                    came_from[number] = (current, through)
                    walk.append(number)
        return None


def read_wait_graph(session: Session) -> WaitGraph:
    """Every issue, initiative and edge as the session reads them, in four queries."""
    graph = WaitGraph()
    issues = session.execute(
        select(models.Issue.id, models.Issue.number, models.Issue.status, models.Issue.parent_id, models.Issue.initiative_id)
    ).all()
    initiative_number = dict(session.execute(select(models.Initiative.id, models.Initiative.number)).all())
    issue_number = {row.id: row.number for row in issues}
    graph.initiatives = set(initiative_number.values())
    for row in issues:
        if row.status not in CLOSED_ISSUE_STATUSES:
            graph.unclosed.add(row.number)
        if row.parent_id is not None:
            graph.parent_of[row.number] = issue_number[row.parent_id]
        if row.initiative_id is not None:
            graph.initiative_of[row.number] = initiative_number[row.initiative_id]
    for issue_id, depends_on_id in session.execute(select(models.IssueDependency.issue_id, models.IssueDependency.depends_on_id)):
        graph.add_dependency(issue_number[issue_id], issue_number[depends_on_id])
    for initiative_id, number in session.execute(
        select(models.InitiativeDependency.initiative_id, models.InitiativeDependency.depends_on_number)
    ):
        graph.add_wait(initiative_number[initiative_id], number)
    return graph


def refuse_wait_loop(graph: WaitGraph, *starts: int) -> None:
    """Answer 409 naming the loop through the first of `starts` that is in one."""
    for start in starts:
        loop = graph.loop_through(start)
        if loop is None:
            continue
        chain = ' → '.join(f'#{number}' for number in loop)
        detail = f'That would leave #{start} waiting on itself: {chain}'
        if graph.initiatives.intersection(loop):
            detail += '. An initiative waits on its unclosed issues, and each of them waits on what the initiative waits on.'
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=detail)
