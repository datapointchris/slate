"""One record's changes: every audit row on the record, and on each row hanging off it.

A row hangs off a record when single-column `ON DELETE CASCADE` foreign keys lead to it from the
record, through any number of rows: a claim on the record's number, then a wait on that claim.
Deleting the record deletes it, so it is part of the record. The schema's foreign keys are the
list, so a table added later is read with no change here.

The audit trigger stores only the columns an update changed, so the update ending a claim names no
record. An insert or a delete holds the whole row. So a row hanging off a record is found by the
parent its insert or its delete names, and every write to that row is then read by its own key.
Audit rows imported with another tracker's rows are found the same way, under the table names they
were written with. A kind's table names in `FORMERLY` the tables its rows lived in before, under the
same ids, so a record's writes there read as its own.
"""

from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass
from typing import Any

from sqlalchemy import ColumnElement
from sqlalchemy import and_
from sqlalchemy import or_
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.database.base import Base
from slate.models.record import Kind
from slate.models.record_change import FORMERLY
from slate.models.record_change import KNOWN_BY
from slate.models.record_change import RECORDED
from slate.services.row_limit import apply_row_limit

Change = models.RecordChange

# Each is a session joining or leaving a worklog, which every hold and resume writes several of.
LEFT_OUT = frozenset({models.WorklogSpan.__tablename__})

# Comments lived on issues alone before documents arrived, in a table no model maps now. History
# written then, or imported from a tracker on that schema, keys each comment by its own id there.
ISSUE_COMMENTS = 'issue_comments'


@dataclass(frozen=True)
class Hanging:
    """A table whose rows hang off a parent, the column in each naming the parent, and whether that key is an integer."""

    table: str
    parent_column: str
    key_is_int: bool


def hanging_tables() -> dict[str, tuple[Hanging, ...]]:
    """Each audited table's children: the tables whose single-column cascading key names its primary key."""
    children: dict[str, list[Hanging]] = defaultdict(list)
    for table in Base.metadata.sorted_tables:
        if not table.info.get(RECORDED, True) or table.name in LEFT_OUT:
            continue
        for constraint in table.foreign_key_constraints:
            if constraint.ondelete != 'CASCADE' or len(constraint.elements) != 1:
                continue
            (key,) = constraint.elements
            parent = key.column.table
            if list(parent.primary_key.columns) == [key.column]:
                children[parent.name].append(Hanging(table.name, key.parent.name, key.column.type.python_type is int))
    children[models.Issue.__tablename__].append(Hanging(ISSUE_COMMENTS, 'issue_id', key_is_int=False))
    return {parent: tuple(links) for parent, links in children.items()}


HANGING = hanging_tables()

# A promotion is the insert of the initiative naming the idea. Only that row is the idea's, because
# every later write to the initiative is the initiative's own.
PROMOTION = Hanging(models.Initiative.__tablename__, 'idea_id', key_is_int=False)


def record_changes(session: Session, record: models.Record, limit: int | None) -> list[schemas.RecordChange]:
    """Every write to `record` and to each row hanging off it, newest first, the latest `limit` of them."""
    kind_model = models.KIND_MODELS[record.kind]
    record_id = str(session.scalars(select(kind_model.id).where(kind_model.number == record.number)).one())
    formerly = Base.metadata.tables[kind_model.__tablename__].info.get(FORMERLY, ())
    own = {(models.Record.__tablename__, str(record.number)), *((table, record_id) for table in (kind_model.__tablename__, *formerly))}
    written = set(own)
    found = own
    while found:
        parents = [(link, int(key) if link.key_is_int else key) for table, key in found for link in HANGING.get(table, ())]
        found = set(rows_naming(session, parents)) - written
        written |= found

    keys_by_table: dict[str, set[str]] = defaultdict(set)
    for table, key in written:
        keys_by_table[table].add(key)
    rows: list[ColumnElement[bool]] = [
        and_(Change.table_name == table, Change.record_id.in_(sorted(keys))) for table, keys in keys_by_table.items()
    ]
    if record.kind == Kind.IDEA:
        rows.append(
            and_(Change.operation == 'insert', Change.table_name == PROMOTION.table, names(PROMOTION.parent_column, record_id, 'new'))
        )
    query = select(Change).where(or_(*rows)).order_by(Change.changed_ts.desc(), Change.id.desc())
    changes = [
        schemas.RecordChange.model_validate(row).model_copy(update={'is_own_row': (row.table_name, row.record_id) in own})
        for row in session.scalars(apply_row_limit(query, limit))
    ]
    return with_referenced_names(session, changes)


@dataclass(frozen=True)
class KnownBy:
    """A table whose rows a person knows by a name, and its columns holding each row's key and that name."""

    table: str
    key: str
    name: str


def references_known_by_name() -> dict[tuple[str, str], KnownBy]:
    """Each column holding the key of a row in a table `KNOWN_BY` marks, by its own table and name."""
    known: dict[tuple[str, str], KnownBy] = {}
    for table in Base.metadata.sorted_tables:
        for constraint in table.foreign_key_constraints:
            if len(constraint.elements) != 1:
                continue
            (key,) = constraint.elements
            referenced = key.column.table
            if (name := referenced.info.get(KNOWN_BY)) is not None:
                known[(table.name, key.parent.name)] = KnownBy(referenced.name, key.column.name, name)
    return known


REFERENCES = references_known_by_name()


def with_referenced_names(session: Session, changes: list[schemas.RecordChange]) -> list[schemas.RecordChange]:
    """Each change with the name of every row it holds the key of, where `KNOWN_BY` says a person knows that row by name."""
    wanted: dict[KnownBy, set[Any]] = defaultdict(set)
    for change in changes:
        for column, sides in change.changes.items():
            if (known := REFERENCES.get((change.table_name, column))) is not None:
                wanted[known].update(value for value in sides.values() if value is not None)
    names: dict[tuple[str, str], str] = {}
    for known, keys in wanted.items():
        table = Base.metadata.tables[known.table]
        current = select(table.c[known.key], table.c[known.name]).where(table.c[known.key].in_(sorted(keys)))
        names.update(((known.table, str(key)), name) for key, name in session.execute(current))
        if gone := sorted(str(key) for key in keys if (known.table, str(key)) not in names):
            deleted = select(Change.record_id, Change.changes[known.name]['old'].astext).where(
                Change.table_name == known.table, Change.operation == 'delete', Change.record_id.in_(gone)
            )
            names.update(((known.table, key), name) for key, name in session.execute(deleted))

    def named(change: schemas.RecordChange) -> dict[str, dict[str, str]]:
        found: dict[str, dict[str, str]] = {}
        for column, sides in change.changes.items():
            if (known := REFERENCES.get((change.table_name, column))) is None:
                continue
            sided = {side: names[(known.table, str(value))] for side, value in sides.items() if (known.table, str(value)) in names}
            if sided:
                found[column] = sided
        return found

    return [change.model_copy(update={'referenced_names': named(change)}) for change in changes]


def names(column: str, key: int | str, side: str) -> ColumnElement[bool]:
    """Whether a write's `side`, `old` or `new`, holds `key` in `column`."""
    return Change.changes.contains({column: {side: key}})


def rows_naming(session: Session, parents: list[tuple[Hanging, int | str]]) -> list[tuple[str, str]]:
    """The table and key of each row whose insert or delete names one of `parents`.

    Each parent is tested once over every table naming it in the same column. Each containment test
    is its own scan of `idx_audit_trail_whole_rows`, so fewer tests keep Postgres on the index.
    """
    if not parents:
        return []
    tables_naming: dict[tuple[str, int | str], list[str]] = defaultdict(list)
    for link, key in parents:
        tables_naming[(link.parent_column, key)].append(link.table)
    named = (
        and_(or_(names(column, key, 'new'), names(column, key, 'old')), Change.table_name.in_(tables))
        for (column, key), tables in tables_naming.items()
    )
    found = session.execute(select(Change.table_name, Change.record_id).distinct().where(Change.operation != 'update', or_(*named)))
    return [(table, key) for table, key in found]
