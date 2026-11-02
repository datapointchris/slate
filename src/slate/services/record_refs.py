"""Resolve the references a person or an agent types for records and labels.

A record of any kind answers to its number, as `N` or `#N`. An issue or an idea also answers
to its UUID, an initiative to its UUID or its name, and a label to its slug. Each endpoint resolves
what it was given inside the request that was going to reach the database anyway.
"""

from uuid import UUID

import structlog
from fastapi import HTTPException
from fastapi import status
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate.api.exceptions import NotFoundException
from slate.services.names_near import names_near

logger = structlog.get_logger()


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
    """Fetch the record a number names, whatever kind holds it."""
    number = ref if isinstance(ref, int) else record_number(ref)
    if number is None:
        raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail=f'{ref!r} is not a record number')
    record = session.get(models.Record, number)
    if record is None:
        raise NotFoundException('record', str(ref), logger)
    return record


def kind_holding(session: Session, number: int | None) -> str | None:
    """The kind of record holding `number`, so a read asking for it as another kind says which holds it."""
    if number is None:
        return None
    record = session.get(models.Record, number)
    return None if record is None else record.kind


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
        raise NotFoundException('issue', str(ref), logger, held_as=kind_holding(session, number))
    return issue


def resolve_idea(session: Session, ref: str | UUID | int) -> models.Idea:
    """Fetch an idea by its UUID or its number."""
    idea: models.Idea | None
    number: int | None = None
    if isinstance(ref, int):
        number = ref
        idea = session.scalar(select(models.Idea).where(models.Idea.number == ref))
    elif isinstance(ref, UUID):
        idea = session.get(models.Idea, ref)
    elif (idea_id := parse_uuid(ref)) is not None:
        idea = session.get(models.Idea, idea_id)
    elif (number := record_number(ref)) is not None:
        idea = session.scalar(select(models.Idea).where(models.Idea.number == number))
    else:
        raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail=f'{ref!r} is not an idea UUID or number')
    if idea is None:
        raise NotFoundException('idea', str(ref), logger, held_as=kind_holding(session, number))
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
        raise NotFoundException('initiative', str(ref), logger, held_as=kind_holding(session, number))
    return initiative


def initiative_names(session: Session) -> list[str]:
    """Every initiative's name, active ones first and each group in board order."""
    query = select(models.Initiative.name).order_by((models.Initiative.status == 'active').desc(), models.Initiative.position)
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
