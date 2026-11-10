"""The tags each record carries, the refusal of a tag nobody made, and the tags an issue reads as.

A record's own tags are its rows in `record_tags`. An issue reads as carrying its own tags plus its
initiative's, whatever the initiative's status, because a category stays true of finished work.
That set is derived on every read, as its effective priority is, so tagging an initiative or moving
an issue into one changes what the issue reads with nothing stored on the issue.
"""

from __future__ import annotations

from collections import defaultdict
from collections.abc import Collection
from collections.abc import Iterable
from dataclasses import dataclass

from fastapi import HTTPException
from fastapi import status
from sqlalchemy import ColumnElement
from sqlalchemy import Select
from sqlalchemy import delete
from sqlalchemy import func
from sqlalchemy import or_
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.record import Kind
from slate.services.names_near import names_near
from slate.services.open_records import is_open
from slate.services.row_limit import apply_row_limit

# The kinds a tag goes on. A pursuit is a grouping of its own rather than a record a category describes.
TAGGED_KINDS = (Kind.IDEA, Kind.INITIATIVE, Kind.ISSUE)


def unknown_tags(session: Session, unknown: list[str]) -> HTTPException:
    """The refusal of tags nobody made, naming every tag that exists and the ones each typed name comes near."""
    known = list(session.scalars(select(models.Tag.name).order_by(models.Tag.name)))
    named = ', '.join(repr(name) for name in unknown)
    detail = f'Unknown tag {named}. A record carries only a tag that exists: {", ".join(known) or "none yet"}.'
    near = list(dict.fromkeys(close for name in unknown for close in names_near(name, known)))
    if near:
        detail += f' Near what was typed: {", ".join(near)}.'
    detail += ' `slate tags create` adds a tag.'
    return HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail=detail)


def resolve_tags(session: Session, names: Iterable[str]) -> list[models.Tag]:
    """Every named tag a write sets, refusing the whole set when any does not exist.

    Each is locked `FOR KEY SHARE` until the write commits, so a delete racing it waits rather than
    failing the membership's key.
    """
    wanted = list(dict.fromkeys(names))
    if not wanted:
        return []
    query = select(models.Tag).where(models.Tag.name.in_(wanted)).with_for_update(key_share=True)
    found = {tag.name: tag for tag in session.scalars(query)}
    if unknown := [name for name in wanted if name not in found]:
        raise unknown_tags(session, unknown)
    return [found[name] for name in wanted]


def resolve_tag(session: Session, name: str) -> models.Tag:
    """The tag a filter names, refused as a write's would be."""
    tag = session.scalar(select(models.Tag).where(models.Tag.name == name))
    if tag is None:
        raise unknown_tags(session, [name])
    return tag


def set_record_tags(session: Session, number: int, tags: Iterable[models.Tag]) -> None:
    """Make the record's own tags exactly `tags`, leaving each row that stays, so its history holds.

    A create resolves its tags before its flush draws the record's number, so a refusal spends none.
    """
    wanted = {tag.id for tag in tags}
    held = set(session.scalars(select(models.RecordTag.tag_id).where(models.RecordTag.record_number == number)))
    if gone := held - wanted:
        session.execute(delete(models.RecordTag).where(models.RecordTag.record_number == number, models.RecordTag.tag_id.in_(sorted(gone))))
    session.add_all(models.RecordTag(record_number=number, tag_id=tag_id) for tag_id in sorted(wanted - held))


def own_tags(session: Session, numbers: Collection[int]) -> dict[int, list[str]]:
    """Each record's own tags by its number, in name order. A record carrying none is left out."""
    if not numbers:
        return {}
    rows = session.execute(
        select(models.RecordTag.record_number, models.Tag.name)
        .join(models.Tag, models.Tag.id == models.RecordTag.tag_id)
        .where(models.RecordTag.record_number.in_(list(numbers)))
        .order_by(models.Tag.name)
    )
    tags: dict[int, list[str]] = defaultdict(list)
    for number, name in rows:
        tags[number].append(name)
    return dict(tags)


@dataclass(frozen=True)
class IssueTags:
    """`own` is what the issue's own `tags` field sets, and `effective` its own plus its initiative's."""

    own: list[str]
    effective: list[str]


def issue_tags(session: Session, issues: Collection[models.Issue]) -> dict[int, IssueTags]:
    """Each issue's tags by its number. Each issue's `initiative` is read, so load it with the issue."""
    initiative_of = {issue.number: issue.initiative.number for issue in issues if issue.initiative is not None}
    tags = own_tags(session, {issue.number for issue in issues} | set(initiative_of.values()))
    return {
        issue.number: IssueTags(
            own=tags.get(issue.number, []),
            effective=sorted({*tags.get(issue.number, []), *tags.get(initiative_of.get(issue.number, 0), [])}),
        )
        for issue in issues
    }


def numbers_carrying(session: Session, name: str) -> Select:
    """The numbers of the records carrying the named tag themselves, refusing a tag that does not exist."""
    tag = resolve_tag(session, name)
    return select(models.RecordTag.record_number).where(models.RecordTag.tag_id == tag.id)


def carries(session: Session, kind: Kind, name: str) -> ColumnElement[bool]:
    """Whether a record of `kind` reads as carrying the tag: its own, or for an issue its initiative's too."""
    carrying = numbers_carrying(session, name)
    model = models.KIND_MODELS[kind]
    if kind != Kind.ISSUE:
        return model.number.in_(carrying)
    in_tagged_initiative = select(models.Initiative.id).where(models.Initiative.number.in_(carrying))
    return or_(models.Issue.number.in_(carrying), models.Issue.initiative_id.in_(in_tagged_initiative))


def narrowed_to_tag(query: Select, kind: Kind, name: str | None, session: Session) -> Select:
    return query if name is None else query.where(carries(session, kind, name))


def tag_views(session: Session, name: str | None = None, limit: int | None = None) -> list[schemas.Tag]:
    """Every tag in name order, or the one named, with how many open records of each kind carry it themselves."""
    tagged = models.RecordTag.record_number
    labels = {kind: f'open_{kind}_count' for kind in TAGGED_KINDS}
    open_counts = [
        func.count(tagged).filter(tagged.in_(select(models.KIND_MODELS[kind].number).where(is_open(kind)))).label(labels[kind])
        for kind in TAGGED_KINDS
    ]
    query = (
        select(models.Tag, *open_counts)
        .outerjoin(models.RecordTag, models.RecordTag.tag_id == models.Tag.id)
        .group_by(models.Tag.id)
        .order_by(models.Tag.name)
    )
    if name is not None:
        query = query.where(models.Tag.name == name)
    views = []
    for row in session.execute(apply_row_limit(query, limit)).all():
        counts = {label: row._mapping[label] for label in labels.values()}
        tag = row.Tag
        views.append(
            schemas.Tag(
                name=tag.name, description=tag.description, created_ts=tag.created_ts, open_record_count=sum(counts.values()), **counts
            )
        )
    return views


def tag_view(session: Session, tag: models.Tag) -> schemas.Tag:
    return tag_views(session, tag.name)[0]


def numbers_tagged(session: Session, tag: models.Tag) -> list[int]:
    """Every record carrying `tag` itself, open or closed, by number."""
    return list(
        session.scalars(
            select(models.RecordTag.record_number).where(models.RecordTag.tag_id == tag.id).order_by(models.RecordTag.record_number)
        )
    )
