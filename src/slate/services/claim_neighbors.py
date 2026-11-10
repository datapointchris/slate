"""What a hold warns about: the record's own state, and each neighbor that is open or claimed.

None of these refuses the hold. Another holder's live claim on the record itself is the one
conflict that does, in `claims.take`, and a pursuit is refused before it, since it holds no work.
A neighbor the same holder claims is that holder's own work, so it goes unnamed.

An issue's neighbors are its initiative, what that initiative waits on, any initiative whose plan
holds it, its parent, its children, what it depends on, what depends on it, and every issue on its
repo naming a path that overlaps one of its own. An initiative's are the issues in its plan. An
idea's is the initiative it became.
"""

from __future__ import annotations

import datetime as dt
from collections import defaultdict
from collections.abc import Callable
from collections.abc import Iterable
from collections.abc import Mapping

from fastapi import HTTPException
from fastapi import status
from sqlalchemy import select
from sqlalchemy.orm import Session
from sqlalchemy.orm import selectinload

from slate import models
from slate.models.initiative import TERMINAL_INITIATIVE_STATUSES
from slate.models.record import Kind
from slate.services.claim_views import held_by
from slate.services.claim_views import live_claims
from slate.services.claim_views import live_claims_on_every_record
from slate.services.held_plans import held_plans
from slate.services.issue_paths import held_overlaps
from slate.services.issue_readiness import measure_readiness
from slate.services.issue_views import ISSUE_LOAD_OPTIONS
from slate.services.record_summaries import record_summaries
from slate.services.record_work import record_work
from slate.services.write_effects import WriteEffectCode
from slate.services.write_effects import WriteEffects

NeighborWarner = Callable[[Session, int, str, dt.datetime, dt.date, WriteEffects], None]


def neighbor_warner(record: models.Record) -> NeighborWarner:
    """What a hold on `record` warns about, read before the hold so a pursuit is refused before any claim is written.

    A pursuit holds no work. A claim on one would take every initiative in it out of the queue.
    """
    match record.kind:
        case Kind.ISSUE:
            return warn_about_issue
        case Kind.INITIATIVE:
            return warn_about_initiative
        case Kind.IDEA:
            return warn_about_idea
        case Kind.PURSUIT:
            raise HTTPException(
                status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
                detail=f'#{record.number} is a pursuit, which holds no work, so no claim holds it. Hold an idea or an initiative in it.',
            )


def claimed_by_others(session: Session, numbers: Iterable[int], holder: str, now: dt.datetime) -> dict[int, models.Claim]:
    return {number: claim for number, claim in live_claims(session, numbers, now).items() if claim.holder != holder}


def numbered(issues: Iterable[models.Issue]) -> str:
    return ', '.join(f'#{issue.number}' for issue in sorted(issues, key=lambda issue: issue.number))


def initiative_named(session: Session, number: int) -> str:
    return f'#{number} {session.scalar(select(models.Initiative.name).where(models.Initiative.number == number))!r}'


def warn_about_passed_over(session: Session, passed_over: Mapping[int, models.Claim], effects: WriteEffects) -> None:
    """Name each plan `POST /claims/next` passed over, so a caller finding nothing to hold knows the work exists and whose it is."""
    by_initiative: defaultdict[int, list[int]] = defaultdict(list)
    claims: dict[int, models.Claim] = {}
    for number, claim in passed_over.items():
        by_initiative[claim.record_number].append(number)
        claims[claim.record_number] = claim
    for initiative, numbers in sorted(by_initiative.items()):
        issues = ', '.join(f'#{number}' for number in sorted(numbers))
        effects.warn(
            WriteEffectCode.PASSED_OVER_PLAN,
            f'Nothing was held. Ready, and in the plan of initiative {initiative_named(session, initiative)}, '
            f'claimed by {held_by(claims[initiative], effects)}: {issues}. '
            f'`slate next --for {initiative}` lists them, and a hold naming one takes it.',
            initiative,
        )


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
        held = f', and claimed by {held_by(claimed[dependency.number], effects)}' if dependency.number in claimed else ''
        effects.warn(
            WriteEffectCode.OPEN_DEPENDENCY,
            f'#{number} depends on #{dependency.number}, which is {dependency.status}{held}.',
            dependency.number,
        )
    waits = [blocking.number for blocking in readiness.blocked_by.get(issue.id, ()) if blocking.through_initiative is not None]
    if waits and issue.initiative is not None:
        initiative = issue.initiative
        for waited in record_summaries(session, waits).values():
            effects.warn(
                WriteEffectCode.INITIATIVE_WAITS,
                f'#{number} is in initiative #{initiative.number} {initiative.name!r}, '
                f'which waits on #{waited.number}, and #{waited.number} is {waited.status}.',
                waited.number,
            )
    if children:
        effects.warn(WriteEffectCode.OPEN_CHILDREN, f'#{number} waits on its open children: {numbered(children)}.', number)
    for child in sorted(children, key=lambda i: i.number):
        if child.number in claimed:
            effects.warn(
                WriteEffectCode.CLAIMED_CHILD,
                f"#{number}'s child #{child.number} is claimed by {held_by(claimed[child.number], effects)}.",
                child.number,
            )
    if issue.parent is not None and issue.parent.number in claimed:
        parent = issue.parent.number
        effects.warn(
            WriteEffectCode.CLAIMED_PARENT, f"#{number}'s parent #{parent} is claimed by {held_by(claimed[parent], effects)}.", parent
        )
    for dependent in dependents:
        if dependent.number in claimed:
            effects.warn(
                WriteEffectCode.CLAIMED_DEPENDENT,
                f'#{dependent.number} depends on #{number}, and is claimed by {held_by(claimed[dependent.number], effects)}.',
                dependent.number,
            )
    if issue.initiative is not None and issue.initiative.number in claimed:
        initiative = issue.initiative
        holding = held_by(claimed[initiative.number], effects)
        effects.warn(
            WriteEffectCode.CLAIMED_INITIATIVE,
            f'#{number} is in initiative #{initiative.number} {initiative.name!r}, which is claimed by {holding}.',
            initiative.number,
        )
    plan_claim = held_plans(session, live_claims_on_every_record(session, now)).get(number)
    own_initiative = issue.initiative.number if issue.initiative is not None else None
    if plan_claim is not None and plan_claim.holder != holder and plan_claim.record_number != own_initiative:
        effects.warn(
            WriteEffectCode.CLAIMED_INITIATIVE,
            f'#{number} is in the plan of initiative {initiative_named(session, plan_claim.record_number)}, '
            f'which is claimed by {held_by(plan_claim, effects)}.',
            plan_claim.record_number,
        )
    for overlap in held_overlaps(session, [issue], now).get(number, []):
        if overlap.claim.holder == holder:
            continue
        effects.warn(
            WriteEffectCode.CLAIMED_OVERLAPPING_PATHS,
            f'#{overlap.number} {overlap.title!r} also changes {", ".join(overlap.paths)} in {issue.repo}, '
            f'and is claimed by {held_by(overlap.claim, effects)}.',
            overlap.number,
        )


def warn_about_initiative(session: Session, number: int, holder: str, now: dt.datetime, today: dt.date, effects: WriteEffects) -> None:
    initiative = session.scalars(select(models.Initiative).where(models.Initiative.number == number)).one()
    if initiative.status in TERMINAL_INITIATIVE_STATUSES:
        effects.warn(WriteEffectCode.RECORD_CLOSED, f'#{number} is {initiative.status}, so a claim on it holds no open work.', number)
    elif initiative.status == 'someday':
        effects.warn(WriteEffectCode.SHELVED, f'#{number} is someday, set aside until it is taken on.', number)
    claimed = claimed_by_others(session, record_work(session, number).unblocks, holder, now)
    for issue_number in sorted(claimed):
        effects.warn(
            WriteEffectCode.CLAIMED_ISSUE_IN_INITIATIVE,
            f'#{issue_number}, in the plan of #{number} {initiative.name!r}, is claimed by {held_by(claimed[issue_number], effects)}.',
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
            f'#{number} became initiative #{initiative.number} {initiative.name!r}, which is claimed by {held_by(claim, effects)}.',
            initiative.number,
        )
