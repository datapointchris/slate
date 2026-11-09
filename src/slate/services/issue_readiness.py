"""Which issues can be taken now, and in what order.

Readiness is derived on every read and never stored, because every input to it
changes elsewhere: a blocker closing, a claim expiring, a deferral day arriving,
a child closing. A stored flag would be wrong the moment any of them moved.

An issue is ready when all of these hold:

- it is `open`, and no live claim holds it; an expired claim holds nothing
- its deferral day, if any, has arrived in the reader's calendar
- every issue it depends on is closed
- its initiative, while open, waits on no open initiative or issue
- it has no open children, since a parent is finished by its children
- it is not shelved: in a `someday` initiative, which holds ideas not yet
  taken on, with no unclosed issue outside one depending on it at any depth

An issue is autofixable when none of it, its initiative and its unclosed parent
says `autofix` is false, and the parent is read the same way up the tree. That
is a person's veto on an unattended fix, and says nothing about whether anyone
may take the issue now, which `ready` answers.

The queue is ordered in slots. A slot is an active initiative with the issues
it holds, or a record standing alone: an idea, an initiative with no issues, or
an issue outside any initiative. Slots sort by pin, then urgency, then their
record's rank. Inside an initiative its issues sort by rank. An issue whose own
priority is more urgent than its initiative's stands alone at its own, unless
the initiative is pinned, which brings all its issues with it. A pinned issue
stands alone at its own pin.

An issue something waits on, a dependent or its parent, sorts just ahead of the
earliest of them when that is earlier than its own place, at any depth. That
is priority inheritance from scheduling: without it a blocker ranked near the
bottom keeps waiting while the work it gates sits at the top of the queue,
hidden because it is blocked. So an issue's effective priority is the urgency
of the place it sorts at: the most urgent of its own, its active initiative's,
and that of everything waiting on it. Nothing in that set lowers it.
"""

from __future__ import annotations

import datetime as dt
from dataclasses import dataclass
from typing import NamedTuple
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate.models.issue import CLOSED_ISSUE_STATUSES
from slate.models.record import Kind
from slate.services.claim_views import live_claim_numbers
from slate.services.initiative_waits import holding_waits
from slate.services.open_records import is_open

# The urgency of "no priority": after every real level, which run 1 (urgent)
# to 4 (low).
NO_PRIORITY_URGENCY = 5

# A slot's own record sorts ahead of whatever the slot holds.
AHEAD_OF_ITS_ISSUES = float('-inf')

# A pinned slot sorts ahead of every unpinned one, whatever its priority.
PINNED = 0
UNPINNED = 1


def urgency(priority: int) -> int:
    return priority or NO_PRIORITY_URGENCY


def priority_of(urgency_value: int) -> int:
    return 0 if urgency_value == NO_PRIORITY_URGENCY else urgency_value


def pin_term(pinned_ts: dt.datetime | None) -> int:
    return UNPINNED if pinned_ts is None else PINNED


class QueueKey(NamedTuple):
    """Where a record sorts. The slot comes first, then the place inside it.

    `ahead` counts how many steps before `inner` a waited-on issue sorts, and
    `tie_rank` and `tie_number` order the issues sorting at the same step.
    """

    pin: int
    urgency: int
    slot_rank: float
    slot_number: int
    inner_rank: float
    inner_number: int
    ahead: int
    tie_rank: float
    tie_number: int

    @property
    def place_before_ties(self) -> tuple:
        """The key up to the tie, which every issue sorting at one step ahead of a waiter shares."""
        return self[:7]


def slot_key(urgency_value: int, rank: float, number: int, pin: int = UNPINNED) -> QueueKey:
    """The key of a record standing as its own slot: an idea, an initiative, or an issue outside one."""
    return QueueKey(pin, urgency_value, rank, number, AHEAD_OF_ITS_ISSUES, 0, 0, 0.0, 0)


def just_ahead_of(waiter: QueueKey, rank: float, number: int) -> QueueKey:
    return waiter._replace(ahead=waiter.ahead - 1, tie_rank=rank, tie_number=number)


class ActiveInitiative(NamedTuple):
    number: int
    pin: int
    urgency: int
    rank: float


@dataclass(frozen=True)
class IssueReadiness:
    effective_priority: dict[UUID, int]
    blocked: frozenset[UUID]
    deferred: frozenset[UUID]
    shelved: frozenset[UUID]
    ready: frozenset[UUID]
    autofixable: frozenset[UUID]
    child_count: dict[UUID, int]
    open_child_count: dict[UUID, int]
    # Every unclosed issue's place, the place it would take on its own, the
    # record number of the slot that place is in, and the issue whose wait put
    # it ahead of its own place.
    queue_key: dict[UUID, QueueKey]
    home_key: dict[UUID, QueueKey]
    slot_of: dict[UUID, int]
    raised_by: dict[UUID, int]
    # Each idea and initiative the queue holds as a slot, by number: an active initiative, with
    # its issues or alone, and an idea nobody has promoted.
    slot_key_by_number: dict[int, QueueKey]

    def sort_key(self, issue: models.Issue) -> QueueKey:
        """A closed issue stands alone at its own priority, which the queue never reads."""
        return self.queue_key.get(issue.id) or slot_key(urgency(issue.priority), issue.rank, issue.number)

    def record_sort_key(self, number: int, priority: int, rank: float) -> QueueKey:
        """An idea or an initiative the queue holds no slot for, such as a held someday one, stands alone at its own priority."""
        return self.slot_key_by_number.get(number) or slot_key(urgency(priority), rank, number)


def measure_readiness(session: Session, today: dt.date, now: dt.datetime) -> IssueReadiness:
    """Read every issue's light columns once and derive the readiness of all of them.

    One pass over the whole table is cheaper than asking per issue, and the
    inheritance walks the graph anyway, so no subset of rows would be enough.
    """
    columns = (
        models.Issue.id,
        models.Issue.number,
        models.Issue.status,
        models.Issue.priority,
        models.Issue.initiative_id,
        models.Issue.parent_id,
        models.Issue.deferred_until_date,
        models.Issue.autofix,
        models.Record.rank,
        models.Record.pinned_ts,
    )
    query = select(*columns).join(models.Record, models.Record.number == models.Issue.number)
    rows = {row.id: row for row in session.execute(query).all()}
    active_initiatives = {
        row.id: ActiveInitiative(row.number, pin_term(row.pinned_ts), urgency(row.priority), row.rank)
        for row in session.execute(
            select(models.Initiative.id, models.Initiative.number, models.Initiative.priority, models.Record.rank, models.Record.pinned_ts)
            .join(models.Record, models.Record.number == models.Initiative.number)
            .where(models.Initiative.status == 'active')
        ).all()
    }
    slot_key_by_number = {
        initiative.number: slot_key(initiative.urgency, initiative.rank, initiative.number, initiative.pin)
        for initiative in active_initiatives.values()
    }
    for number, priority, rank, pinned_ts in session.execute(
        select(models.Idea.number, models.Idea.priority, models.Record.rank, models.Record.pinned_ts)
        .join(models.Record, models.Record.number == models.Idea.number)
        .where(is_open(Kind.IDEA))
    ).all():
        slot_key_by_number[number] = slot_key(urgency(priority), rank, number, pin_term(pinned_ts))
    someday_initiatives = set(session.scalars(select(models.Initiative.id).where(models.Initiative.status == 'someday')).all())
    no_autofix_initiatives = set(session.scalars(select(models.Initiative.id).where(models.Initiative.autofix.is_(False))).all())
    edges = session.execute(select(models.IssueDependency.issue_id, models.IssueDependency.depends_on_id)).all()

    unclosed = {issue_id for issue_id, row in rows.items() if row.status not in CLOSED_ISSUE_STATUSES}

    dependents: dict[UUID, list[UUID]] = {}
    blocked: set[UUID] = set()
    for issue_id, depends_on_id in edges:
        dependents.setdefault(depends_on_id, []).append(issue_id)
        if issue_id in unclosed and depends_on_id in unclosed:
            blocked.add(issue_id)
    waiting_initiatives = {wait.initiative_id for wait in holding_waits(session)}
    blocked.update(issue_id for issue_id in unclosed if rows[issue_id].initiative_id in waiting_initiatives)

    child_count: dict[UUID, int] = {}
    open_child_count: dict[UUID, int] = {}
    for issue_id, row in rows.items():
        if row.parent_id is not None:
            child_count[row.parent_id] = child_count.get(row.parent_id, 0) + 1
            if issue_id in unclosed:
                open_child_count[row.parent_id] = open_child_count.get(row.parent_id, 0) + 1

    home_key: dict[UUID, QueueKey] = {}
    slot_of: dict[UUID, int] = {}
    for issue_id in unclosed:
        row = rows[issue_id]
        initiative = active_initiatives.get(row.initiative_id)
        stays_inside = initiative is not None and (initiative.pin == PINNED or urgency(row.priority) >= initiative.urgency)
        if initiative is not None and stays_inside and row.pinned_ts is None:
            home_key[issue_id] = QueueKey(
                initiative.pin, initiative.urgency, initiative.rank, initiative.number, row.rank, row.number, 0, 0.0, 0
            )
            slot_of[issue_id] = initiative.number
        else:
            home_key[issue_id] = slot_key(urgency(row.priority), row.rank, row.number, pin_term(row.pinned_ts))
            slot_of[issue_id] = row.number

    queue_key: dict[UUID, QueueKey] = {}
    raised_by: dict[UUID, int] = {}
    walking: set[UUID] = set()

    def place(issue_id: UUID) -> QueueKey | None:
        if issue_id in queue_key:
            return queue_key[issue_id]
        # Every loop through a dependency or a parent edge is refused on write, so
        # re-entering means a row written around that check. Contributing nothing
        # keeps the walk finite.
        if issue_id in walking:
            return None
        walking.add(issue_id)
        row = rows[issue_id]
        best = home_key[issue_id]
        waiters = [dependent_id for dependent_id in dependents.get(issue_id, []) if dependent_id in unclosed]
        if row.parent_id in unclosed:
            waiters.append(row.parent_id)
        for waiter_id in waiters:
            waiter = place(waiter_id)
            if waiter is not None and (ahead := just_ahead_of(waiter, row.rank, row.number)) < best:
                best = ahead
                raised_by[issue_id] = rows[waiter_id].number
        walking.discard(issue_id)
        queue_key[issue_id] = best
        return best

    for issue_id in unclosed:
        place(issue_id)

    effective_priority = {
        issue_id: priority_of(queue_key[issue_id].urgency) if issue_id in unclosed else row.priority for issue_id, row in rows.items()
    }

    deferred = {
        issue_id for issue_id in unclosed if rows[issue_id].deferred_until_date is not None and rows[issue_id].deferred_until_date > today
    }

    shelved_memo: dict[UUID, bool] = {}

    def is_shelved(issue_id: UUID) -> bool:
        if issue_id in shelved_memo:
            return shelved_memo[issue_id]
        # Seeded before the walk, so a dependency loop written around the write check ends.
        shelved_memo[issue_id] = True
        result = rows[issue_id].initiative_id in someday_initiatives and not any(
            dependent_id in unclosed and not is_shelved(dependent_id) for dependent_id in dependents.get(issue_id, [])
        )
        shelved_memo[issue_id] = result
        return result

    shelved = {issue_id for issue_id in unclosed if is_shelved(issue_id)}

    held = live_claim_numbers(session, now)
    ready = set()
    for issue_id in unclosed:
        row = rows[issue_id]
        if (
            row.status == 'open'
            and row.number not in held
            and issue_id not in deferred
            and issue_id not in blocked
            and not open_child_count.get(issue_id)
            and issue_id not in shelved
        ):
            ready.add(issue_id)

    withheld_memo: dict[UUID, bool] = {}

    def withheld(issue_id: UUID) -> bool:
        if issue_id in withheld_memo:
            return withheld_memo[issue_id]
        # Seeded before the walk, so a parent loop written around the write check ends.
        withheld_memo[issue_id] = False
        row = rows[issue_id]
        result = not row.autofix or row.initiative_id in no_autofix_initiatives or (row.parent_id in unclosed and withheld(row.parent_id))
        withheld_memo[issue_id] = result
        return result

    autofixable = {issue_id for issue_id in rows if not withheld(issue_id)}

    return IssueReadiness(
        effective_priority=effective_priority,
        blocked=frozenset(blocked),
        deferred=frozenset(deferred),
        shelved=frozenset(shelved),
        ready=frozenset(ready),
        autofixable=frozenset(autofixable),
        child_count=child_count,
        open_child_count=open_child_count,
        queue_key=queue_key,
        home_key=home_key,
        slot_of=slot_of,
        raised_by=raised_by,
        slot_key_by_number=slot_key_by_number,
    )
