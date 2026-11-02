"""What a hold warns about: the record's own state, and each neighbor that is open or claimed.

None of these refuses the hold. Another holder's live claim on the record itself is the one
conflict that does, in `claims.take`. A neighbor the same holder claims is that holder's own work,
so it goes unnamed.

An issue's neighbors are its initiative, its parent, its children, what it depends on and what
depends on it. An initiative's are its unclosed issues. An idea's is the initiative it became.
"""

from __future__ import annotations

import datetime as dt
from collections.abc import Callable
from collections.abc import Iterable

from sqlalchemy import select
from sqlalchemy.orm import Session
from sqlalchemy.orm import selectinload

from slate import models
from slate.models.initiative import TERMINAL_INITIATIVE_STATUSES
from slate.models.issue import CLOSED_ISSUE_STATUSES
from slate.models.record import Kind
from slate.services.claim_views import held_by
from slate.services.claim_views import live_claims
from slate.services.issue_readiness import measure_readiness
from slate.services.issue_views import ISSUE_LOAD_OPTIONS
from slate.services.write_effects import WriteEffectCode
from slate.services.write_effects import WriteEffects

NeighborWarner = Callable[[Session, int, str, dt.datetime, dt.date, WriteEffects], None]


def neighbor_warner(kind: Kind) -> NeighborWarner:
    match kind:
        case Kind.ISSUE:
            return warn_about_issue
        case Kind.INITIATIVE:
            return warn_about_initiative
        case Kind.IDEA:
            return warn_about_idea


def warn_about_neighbors(
    session: Session, record: models.Record, holder: str, now: dt.datetime, today: dt.date, effects: WriteEffects
) -> None:
    neighbor_warner(record.kind)(session, record.number, holder, now, today, effects)


def claimed_by_others(session: Session, numbers: Iterable[int], holder: str, now: dt.datetime) -> dict[int, models.Claim]:
    return {number: claim for number, claim in live_claims(session, numbers, now).items() if claim.holder != holder}


def numbered(issues: Iterable[models.Issue]) -> str:
    return ', '.join(f'#{issue.number}' for issue in sorted(issues, key=lambda issue: issue.number))


def warn_about_issue(session: Session, number: int, holder: str, now: dt.datetime, today: dt.date, effects: WriteEffects) -> None:
    issue = session.scalars(
        select(models.Issue).options(*ISSUE_LOAD_OPTIONS, selectinload(models.Issue.children)).where(models.Issue.number == number)
    ).one()
    readiness = measure_readiness(session, today, now)
    if issue.is_closed:
        effects.warn(WriteEffectCode.RECORD_CLOSED, f'#{number} is {issue.status}, so a claim on it holds no open work.', number)
    elif issue.status == 'triage':
        effects.warn(WriteEffectCode.IN_TRIAGE, f'#{number} is in triage, so nobody has accepted it as work yet.', number)
    if issue.id in readiness.deferred:
        effects.warn(WriteEffectCode.DEFERRED, f'#{number} is deferred until {issue.deferred_until_date}.', number)
    if issue.id in readiness.shelved and issue.initiative is not None:
        effects.warn(
            WriteEffectCode.SHELVED,
            f'#{number} is in the someday initiative {issue.initiative.name!r}, and nothing outside someday depends on it.',
            number,
        )

    dependencies = sorted((edge.depends_on for edge in issue.dependencies if not edge.depends_on.is_closed), key=lambda i: i.number)
    children = [child for child in issue.children if not child.is_closed]
    dependents = sorted((edge.issue for edge in issue.dependents if not edge.issue.is_closed), key=lambda i: i.number)
    neighbors = [neighbor.number for neighbor in (*dependencies, *children, *dependents, issue.parent, issue.initiative) if neighbor]
    claimed = claimed_by_others(session, neighbors, holder, now)

    for dependency in dependencies:
        held = f', and claimed by {held_by(claimed[dependency.number])}' if dependency.number in claimed else ''
        effects.warn(
            WriteEffectCode.OPEN_DEPENDENCY,
            f'#{number} depends on #{dependency.number}, which is {dependency.status}{held}.',
            dependency.number,
        )
    if children:
        effects.warn(WriteEffectCode.OPEN_CHILDREN, f'#{number} waits on its open children: {numbered(children)}.', number)
    for child in sorted(children, key=lambda i: i.number):
        if child.number in claimed:
            effects.warn(
                WriteEffectCode.CLAIMED_CHILD,
                f"#{number}'s child #{child.number} is claimed by {held_by(claimed[child.number])}.",
                child.number,
            )
    if issue.parent is not None and issue.parent.number in claimed:
        parent = issue.parent.number
        effects.warn(WriteEffectCode.CLAIMED_PARENT, f"#{number}'s parent #{parent} is claimed by {held_by(claimed[parent])}.", parent)
    for dependent in dependents:
        if dependent.number in claimed:
            effects.warn(
                WriteEffectCode.CLAIMED_DEPENDENT,
                f'#{dependent.number} depends on #{number}, and is claimed by {held_by(claimed[dependent.number])}.',
                dependent.number,
            )
    if issue.initiative is not None and issue.initiative.number in claimed:
        initiative = issue.initiative
        holding = held_by(claimed[initiative.number])
        effects.warn(
            WriteEffectCode.CLAIMED_INITIATIVE,
            f'#{number} is in initiative #{initiative.number} {initiative.name!r}, which is claimed by {holding}.',
            initiative.number,
        )


def warn_about_initiative(session: Session, number: int, holder: str, now: dt.datetime, today: dt.date, effects: WriteEffects) -> None:
    initiative = session.scalars(select(models.Initiative).where(models.Initiative.number == number)).one()
    if initiative.status in TERMINAL_INITIATIVE_STATUSES:
        effects.warn(WriteEffectCode.RECORD_CLOSED, f'#{number} is {initiative.status}, so a claim on it holds no open work.', number)
    elif initiative.status == 'someday':
        effects.warn(WriteEffectCode.SHELVED, f'#{number} is someday, set aside until it is taken on.', number)
    unclosed = session.scalars(
        select(models.Issue.number).where(models.Issue.initiative_id == initiative.id, models.Issue.status.not_in(CLOSED_ISSUE_STATUSES))
    ).all()
    claimed = claimed_by_others(session, unclosed, holder, now)
    for issue_number in sorted(claimed):
        effects.warn(
            WriteEffectCode.CLAIMED_ISSUE_IN_INITIATIVE,
            f'#{issue_number}, in #{number} {initiative.name!r}, is claimed by {held_by(claimed[issue_number])}.',
            issue_number,
        )


def warn_about_idea(session: Session, number: int, holder: str, now: dt.datetime, today: dt.date, effects: WriteEffects) -> None:
    initiative = session.scalars(select(models.Initiative).join(models.Idea).where(models.Idea.number == number)).one_or_none()
    if initiative is None:
        return
    effects.warn(
        WriteEffectCode.IDEA_PROMOTED,
        f'#{number} was promoted to initiative #{initiative.number} {initiative.name!r}, where its work is.',
        initiative.number,
    )
    if (claim := claimed_by_others(session, [initiative.number], holder, now).get(initiative.number)) is not None:
        effects.warn(
            WriteEffectCode.CLAIMED_INITIATIVE,
            f'#{number} became initiative #{initiative.number} {initiative.name!r}, which is claimed by {held_by(claim)}.',
            initiative.number,
        )
