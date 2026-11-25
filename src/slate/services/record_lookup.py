"""Resolve the references a person or an agent types for records and labels.

A record of any kind answers to its number, as `N` or `#N`. An issue or an idea also answers
to its UUID, an initiative or a pursuit to its UUID or its name, and a label to its slug. Each endpoint resolves
what it was given inside the request that was going to reach the database anyway.

A number or an idea's UUID that a fold retired answers as the record its rows went to, through
`record_redirects`, in a read of any kind or of the target's kind. HubSpot answers a merged
record's old id with the survivor the same way. Only a request that asks follows the redirect.
A client from before the fold reads a record and then writes by the `id` the read answered, so
following for it would point that write at the target. Every other read answers 404 naming where
the number leads. A write through a retired number answers 409 naming the target, because the
caller named a record that no longer exists.
"""

from uuid import UUID
from zoneinfo import ZoneInfo

import structlog
from fastapi import HTTPException
from fastapi import status
from sqlalchemy import select
from sqlalchemy import union_all
from sqlalchemy.orm import Session

from slate import models
from slate.api.exceptions import NotFoundException
from slate.api.exceptions import with_article
from slate.api.request_zone import CALENDAR_FALLBACK_ZONE
from slate.models.record import Kind
from slate.models.record_redirect import FOLDED
from slate.services.names_near import names_near

logger = structlog.get_logger()

# What `mark_request` records on the session before any route resolves a record: whether the
# request writes, whether it asks to follow a redirect, and the zone its reader reads a redirect's day in.
REQUEST_WRITES = 'slate.request_writes'
REQUEST_FOLLOWS = 'slate.request_follows'
REQUEST_ZONE = 'slate.request_zone'


def mark_request(session: Session, writes: bool, follows: bool, zone: str) -> None:
    """Record how a redirect met on `session` answers: a write is refused, and a read follows it only where it asked."""
    session.info[REQUEST_WRITES] = writes
    session.info[REQUEST_FOLLOWS] = follows
    session.info[REQUEST_ZONE] = zone


def parse_uuid(ref: str) -> UUID | None:
    try:
        return UUID(ref)
    except ValueError:
        return None


def record_number(ref: str) -> int | None:
    """The record number `ref` names as `N` or `#N`, or None when it names none."""
    digits = ref.removeprefix('#')
    if digits.isascii() and digits.isdecimal():
        return int(digits)
    return None


def resolve_record(session: Session, ref: str | int) -> models.Record:
    """Fetch the record a number names, whatever kind holds it, following a redirect where a fold retired the number."""
    number = ref if isinstance(ref, int) else record_number(ref)
    if number is None:
        raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail=f'{ref!r} is not a record number')
    record = session.get(models.Record, number)
    if record is not None:
        return record
    redirect = redirect_of(session, number)
    if redirect is None:
        raise NotFoundException('record', str(ref), logger)
    return follow(session, redirect, 'record', ref, None)


def kind_holding(session: Session, number: int | None) -> str | None:
    """The kind of record holding `number`, so a read asking for it as another kind says which holds it."""
    if number is None:
        return None
    record = session.get(models.Record, number)
    return None if record is None else record.kind


def number_holding_id(session: Session, record_id: UUID) -> int | None:
    """The number of the record whose kind row holds `record_id`. A promotion keeps the idea's `id` on the new kind's row."""
    return session.scalar(union_all(*(select(model.number).where(model.id == record_id) for model in models.KIND_MODELS.values())))


def redirect_of(session: Session, number: int | None = None, record_id: UUID | None = None) -> models.RecordRedirect | None:
    """The redirect leading a retired number, or the `id` of the idea that held it, to the record holding its rows."""
    if number is not None:
        return session.get(models.RecordRedirect, number)
    if record_id is not None:
        return session.scalar(select(models.RecordRedirect).where(models.RecordRedirect.former_id == record_id))
    return None


def redirect_named(session: Session, ref: str | int | UUID) -> models.RecordRedirect | None:
    """The redirect that a number or an idea's UUID names, or None where the ref names a record that still holds it."""
    if isinstance(ref, UUID):
        return redirect_of(session, record_id=ref)
    if isinstance(ref, int):
        return redirect_of(session, ref)
    if (record_id := parse_uuid(ref)) is not None:
        return redirect_of(session, record_id=record_id)
    return redirect_of(session, record_number(ref))


def redirect_sentence(session: Session, redirect: models.RecordRedirect, target_kind: str) -> str:
    """Where the number leads and since when, as `#4086 was folded into initiative #2356 on 2026-11-24`, on the reader's calendar."""
    how = 'folded into' if redirect.reason == FOLDED else 'promoted to'
    day = redirect.redirected_ts.astimezone(ZoneInfo(session.info.get(REQUEST_ZONE, CALENDAR_FALLBACK_ZONE))).date()
    return f'#{redirect.number} was {how} {target_kind} #{redirect.to_number} on {day.isoformat()}'


def follow(session: Session, redirect: models.RecordRedirect, resource: str, ref: object, kind: Kind | None) -> models.Record:
    """The record `redirect` leads to, which a read that asked to follow answers where it asked for any kind, or for the target's.

    A write through the redirect answers 409 naming the target, so the caller repeats it there. A read
    that did not ask, or that asked for another kind, answers 404 naming where the number leads.
    """
    target = session.get(models.Record, redirect.to_number)
    if target is None:
        raise NotFoundException(resource, str(ref), logger)
    sentence = redirect_sentence(session, redirect, target.kind)
    if session.info.get(REQUEST_WRITES):
        logger.info('write_through_redirect_refused', number=redirect.number, to_number=redirect.to_number)
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=f'{sentence}; run it against #{redirect.to_number}')
    if not session.info.get(REQUEST_FOLLOWS) or (kind is not None and target.kind != kind):
        raise NotFoundException(resource, str(ref), logger, led_to=sentence)
    return target


def missed(session: Session, resource: str, ref: object, kind: Kind, number: int | None = None, record_id: UUID | None = None) -> int:
    """The number of the `kind` record a redirect leads `number` or `record_id` to, after a lookup of `kind` found none.

    Where no redirect leads anywhere, 404 names the kind holding the number, so `issue 1986 not found`
    says that #1986 is an initiative.
    """
    redirect = redirect_of(session, number, record_id)
    if redirect is not None:
        return follow(session, redirect, resource, ref, kind).number
    held_number = None
    if number is None and record_id is not None:
        number = held_number = number_holding_id(session, record_id)
    raise NotFoundException(resource, str(ref), logger, held_as=kind_holding(session, number), held_number=held_number)


def issue_by_number(session: Session, number: int) -> models.Issue | None:
    return session.scalar(select(models.Issue).where(models.Issue.number == number))


def resolve_issue(session: Session, ref: str | UUID | int) -> models.Issue:
    """Fetch an issue by its UUID or its number."""
    issue: models.Issue | None
    number: int | None = None
    if isinstance(ref, int):
        number = ref
        issue = issue_by_number(session, ref)
    elif isinstance(ref, UUID):
        issue = session.get(models.Issue, ref)
    # A hyphenless UUID can be 32 decimal digits, so it is parsed before the
    # all-digits test. No number will ever be that long.
    elif (issue_id := parse_uuid(ref)) is not None:
        issue = session.get(models.Issue, issue_id)
    elif (number := record_number(ref)) is not None:
        issue = issue_by_number(session, number)
    else:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail=f'{ref!r} is not an issue UUID or number',
        )
    if issue is None:
        record_id = ref if isinstance(ref, UUID) else parse_uuid(ref) if isinstance(ref, str) else None
        return session.scalars(
            select(models.Issue).where(models.Issue.number == missed(session, 'issue', ref, Kind.ISSUE, number, record_id))
        ).one()
    return issue


def resolve_idea(session: Session, ref: str | UUID | int) -> models.Idea:
    """Fetch an idea by its UUID or its number."""
    idea: models.Idea | None
    number: int | None = None
    idea_id: UUID | None = None
    if isinstance(ref, int):
        number = ref
        idea = session.scalar(select(models.Idea).where(models.Idea.number == ref))
    elif isinstance(ref, UUID):
        idea_id = ref
        idea = session.get(models.Idea, ref)
    elif (idea_id := parse_uuid(ref)) is not None:
        idea = session.get(models.Idea, idea_id)
    elif (number := record_number(ref)) is not None:
        idea = session.scalar(select(models.Idea).where(models.Idea.number == number))
    else:
        raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail=f'{ref!r} is not an idea UUID or number')
    if idea is None:
        return session.scalars(
            select(models.Idea).where(models.Idea.number == missed(session, 'idea', ref, Kind.IDEA, number, idea_id))
        ).one()
    return idea


def initiative_by_name(session: Session, name: str) -> models.Initiative | None:
    """The active initiative holding the name, else a lone inactive one, else ambiguity named."""
    matches = session.scalars(select(models.Initiative).where(models.Initiative.name == name)).all()
    active = [initiative for initiative in matches if initiative.status == 'active']
    if active:
        return active[0]
    if len(matches) == 1:
        return matches[0]
    if matches:
        candidates = ', '.join(f'{initiative.id} ({initiative.status})' for initiative in sorted(matches, key=lambda i: i.status))
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'{name!r} names {len(matches)} initiatives and no active one. Use an id: {candidates}',
        )
    return None


def initiative_by_number(session: Session, number: int) -> models.Initiative | None:
    return session.scalar(select(models.Initiative).where(models.Initiative.number == number))


def resolve_initiative(session: Session, ref: str | UUID | int) -> models.Initiative:
    """Fetch an initiative by its UUID, its number or its name."""
    initiative: models.Initiative | None
    number: int | None = None
    if isinstance(ref, int):
        number = ref
        initiative = initiative_by_number(session, ref)
    elif isinstance(ref, UUID):
        initiative = session.get(models.Initiative, ref)
    elif (initiative_id := parse_uuid(ref)) is not None:
        initiative = session.get(models.Initiative, initiative_id)
    elif (number := record_number(ref)) is not None:
        initiative = initiative_by_number(session, number)
    else:
        initiative = initiative_by_name(session, ref)
        if initiative is None:
            raise NotFoundException('initiative', ref, logger, near=names_near(ref, initiative_names(session)))
    if initiative is None:
        record_id = ref if isinstance(ref, UUID) else parse_uuid(ref) if isinstance(ref, str) else None
        return session.scalars(
            select(models.Initiative).where(
                models.Initiative.number == missed(session, 'initiative', ref, Kind.INITIATIVE, number, record_id)
            )
        ).one()
    return initiative


def resolve_issue_or_initiative(session: Session, ref: str | UUID | int) -> models.Issue | models.Initiative:
    """Fetch an issue or an initiative by number or UUID, or an initiative by name, as a wait names either end."""
    number = ref if isinstance(ref, int) else None if isinstance(ref, UUID) else record_number(ref)
    record_id = ref if isinstance(ref, UUID) else parse_uuid(ref) if isinstance(ref, str) else None
    if number is not None:
        record = resolve_record(session, number)
        if record.kind not in (Kind.ISSUE, Kind.INITIATIVE):
            hint = ' Promote the idea first.' if record.kind == Kind.IDEA else ''
            raise HTTPException(
                status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
                detail=f'#{number} is {with_article(record.kind)}, and only issues and initiatives wait or are waited on.{hint}',
            )
        found = issue_by_number(session, number) if record.kind == Kind.ISSUE else initiative_by_number(session, number)
    elif record_id is not None:
        found = session.get(models.Issue, record_id) or session.get(models.Initiative, record_id)
        if found is None and (redirect := redirect_of(session, record_id=record_id)) is not None:
            return resolve_issue_or_initiative(session, follow(session, redirect, 'issue or initiative', ref, None).number)
    else:
        return resolve_initiative(session, ref)
    if found is None:
        raise NotFoundException('issue or initiative', str(ref), logger)
    return found


def initiative_names(session: Session) -> list[str]:
    """Every initiative's name, active ones first and each group in queue order."""
    query = select(models.Initiative.name).order_by((models.Initiative.status == 'active').desc(), models.Initiative.rank)
    return list(session.scalars(query))


def pursuit_by_name(session: Session, name: str) -> models.Pursuit | None:
    """The active pursuit holding the name, else a lone inactive one, else ambiguity named."""
    matches = session.scalars(select(models.Pursuit).where(models.Pursuit.name == name).order_by(models.Pursuit.number)).all()
    active = [pursuit for pursuit in matches if pursuit.status == 'active']
    if active:
        return active[0]
    if len(matches) == 1:
        return matches[0]
    if matches:
        candidates = ', '.join(f'#{pursuit.number} ({pursuit.status})' for pursuit in matches)
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'{name!r} names {len(matches)} pursuits and no active one. Use a number: {candidates}',
        )
    return None


def find_pursuit(session: Session, ref: str | UUID | int) -> models.Pursuit | None:
    """The pursuit a number, a UUID or an exact name names, or None where it names none."""
    if isinstance(ref, int):
        return session.scalar(select(models.Pursuit).where(models.Pursuit.number == ref))
    if isinstance(ref, UUID):
        return session.get(models.Pursuit, ref)
    if (pursuit_id := parse_uuid(ref)) is not None:
        return session.get(models.Pursuit, pursuit_id)
    if (number := record_number(ref)) is not None:
        return session.scalar(select(models.Pursuit).where(models.Pursuit.number == number))
    return pursuit_by_name(session, ref)


def resolve_pursuit(session: Session, ref: str | UUID | int) -> models.Pursuit:
    """Fetch a pursuit by its number, its UUID or its exact name."""
    pursuit = find_pursuit(session, ref)
    if pursuit is not None:
        return pursuit
    number = ref if isinstance(ref, int) else None if isinstance(ref, UUID) else record_number(ref)
    record_id = ref if isinstance(ref, UUID) else parse_uuid(ref) if isinstance(ref, str) else None
    if number is None and record_id is None and isinstance(ref, str):
        raise NotFoundException('pursuit', ref, logger, near=names_near(ref, pursuit_names(session)))
    missed(session, 'pursuit', ref, Kind.PURSUIT, number, record_id)
    raise NotFoundException('pursuit', str(ref), logger)


def pursuit_names(session: Session) -> list[str]:
    """Every pursuit's name, active ones first and each group in number order."""
    query = select(models.Pursuit.name).order_by((models.Pursuit.status == 'active').desc(), models.Pursuit.number)
    return list(session.scalars(query))


def resolve_label(session: Session, slug: str) -> models.IssueLabel:
    label = session.scalar(select(models.IssueLabel).where(models.IssueLabel.slug == slug))
    if label is None:
        raise NotFoundException('label', slug, logger)
    return label


def resolve_labels(session: Session, slugs: list[str]) -> list[models.IssueLabel]:
    """Fetch every named label, refusing the set if any is unknown or two share a group.

    The vocabulary is closed, so an unknown slug is a typo or a label nobody has
    defined yet, and either way it is named rather than created. Labels in one
    group exclude each other.
    """
    wanted = list(dict.fromkeys(slugs))
    found = {label.slug: label for label in session.scalars(select(models.IssueLabel).where(models.IssueLabel.slug.in_(wanted)))}
    unknown = [slug for slug in wanted if slug not in found]
    if unknown:
        known = session.scalars(select(models.IssueLabel.slug).order_by(models.IssueLabel.slug)).all()
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail=f'Unknown label {", ".join(unknown)}. Known labels: {", ".join(known) or "none yet"}',
        )
    labels = [found[slug] for slug in wanted]
    by_group: dict[str, list[str]] = {}
    for label in labels:
        if label.group_slug is not None:
            by_group.setdefault(label.group_slug, []).append(label.slug)
    clashes = {group: members for group, members in by_group.items() if len(members) > 1}
    if clashes:
        described = '; '.join(f'{group}: {", ".join(members)}' for group, members in sorted(clashes.items()))
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail=f'An issue takes one label per group. Conflicting: {described}',
        )
    return labels
