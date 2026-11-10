"""One record's changes: every audit row on the record, and on each row hanging off it.

A row hangs off a record when single-column `ON DELETE CASCADE` foreign keys lead to it from the
record, through any number of rows: a claim on the record's number, then a wait on that claim.
Deleting the record deletes it, so it is part of the record. The schema's foreign keys are the
list, so a table added later is read with no change here.

The audit trigger stores only the columns an update changed, so the update ending a claim names no
record. An insert or a delete holds the whole row. So a row hanging off a record is found by the
parent its insert or its delete names, and every write to that row is then read by its own key.
Audit rows imported with another tracker's rows are found the same way, under the table names they
were written with.
"""

from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass

from sqlalchemy import ColumnElement
from sqlalchemy import and_
from sqlalchemy import or_
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.database.base import Base
from slate.models.record import Kind
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
    own = {(models.Record.__tablename__, str(record.number)), (kind_model.__tablename__, record_id)}
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
    return [
        schemas.RecordChange.model_validate(row).model_copy(update={'is_own_row': (row.table_name, row.record_id) in own})
        for row in session.scalars(apply_row_limit(query, limit))
    ]


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
