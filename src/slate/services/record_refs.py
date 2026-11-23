"""Refs on any record: link them, read them, carry an idea's onto its initiative, and narrow a list by prefix.

An idea, an initiative and an issue each carry refs through `record_refs`, keyed on the record's
number. slate stores a ref as given and never follows it.
"""

from __future__ import annotations

from collections import defaultdict
from collections.abc import Collection
from collections.abc import Iterable
from urllib.parse import urlsplit

from sqlalchemy import Select
from sqlalchemy import select
from sqlalchemy.dialects.postgresql import insert
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.record import Kind
from slate.schemas.record_ref import RefKind
from slate.services.write_effects import WriteEffectCode
from slate.services.write_effects import WriteEffects


def ref_kind(ref: str) -> RefKind:
    parts = urlsplit(ref)
    return 'url' if parts.scheme in ('http', 'https') and parts.netloc else 'key'


def ref_view(row: models.RecordRef) -> schemas.RecordRef:
    return schemas.RecordRef(ref=row.ref, kind=ref_kind(row.ref), note=row.note, linked_by=row.linked_by, linked_ts=row.linked_ts)


def refs_of(session: Session, numbers: Collection[int]) -> dict[int, list[schemas.RecordRef]]:
    """Each record's refs by its number, oldest first. A record carrying none is left out."""
    if not numbers:
        return {}
    rows = session.scalars(
        select(models.RecordRef)
        .where(models.RecordRef.record_number.in_(list(numbers)))
        .order_by(models.RecordRef.linked_ts, models.RecordRef.ref)
    )
    refs: dict[int, list[schemas.RecordRef]] = defaultdict(list)
    for row in rows:
        refs[row.record_number].append(ref_view(row))
    return dict(refs)


def link_refs(session: Session, number: int, refs: Iterable[str], note: str | None, linked_by: str, effects: WriteEffects) -> None:
    """Link each ref to record `number` once. A ref that it already carries keeps its note and linker, and the answer warns.

    The insert skips a ref already carried rather than reading first, so two callers linking one ref at once both succeed.
    """
    wanted = list(dict.fromkeys(refs))
    if not wanted:
        return
    rows = [{'record_number': number, 'ref': ref, 'note': note, 'linked_by': linked_by} for ref in wanted]
    linked = set(session.scalars(insert(models.RecordRef).values(rows).on_conflict_do_nothing().returning(models.RecordRef.ref)))
    for ref in wanted:
        if ref not in linked:
            effects.warn(WriteEffectCode.REF_ALREADY_LINKED, f'#{number} already carries {ref!r}, so it is left as it was.', number)


def carry_refs(session: Session, source: int, target: int, effects: WriteEffects) -> None:
    """Copy each ref of record `source` onto record `target`, which carries none yet, as it was linked."""
    rows = session.scalars(select(models.RecordRef).where(models.RecordRef.record_number == source)).all()
    if not rows:
        return
    for row in rows:
        session.add(models.RecordRef(record_number=target, ref=row.ref, note=row.note, linked_by=row.linked_by, linked_ts=row.linked_ts))
    session.flush()
    count = len(rows)
    effects.did(WriteEffectCode.REFS_CARRIED, f'Carried {count} ref{"" if count == 1 else "s"} from #{source} onto #{target}.', target)


def narrowed_to_ref_prefix(query: Select, kind: Kind, prefix: str | None) -> Select:
    """Narrow a read of `kind` to the records carrying a ref that starts with `prefix`."""
    if not prefix:
        return query
    carrying = select(models.RecordRef.record_number).where(models.RecordRef.ref.startswith(prefix, autoescape=True))
    return query.where(models.KIND_MODELS[kind].number.in_(carrying))
