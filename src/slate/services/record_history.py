"""One record's history: every audit row on the record, and on each row hanging off it.

The audit trigger stores only the columns an update changed, so the update ending a claim names no
record. An insert or a delete holds the whole row. So a row hanging off a record is found by the
parent its insert or its delete names, and every write to that row is then read by its own key.
A row imported with another tracker's history is found the same way, under the table names it
was written with.
"""

from __future__ import annotations

from dataclasses import dataclass

from sqlalchemy import and_
from sqlalchemy import or_
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.record import Kind
from slate.services.row_limit import apply_row_limit

Change = models.RecordChange

# Comments lived on issues alone before documents arrived, in a table no model maps now. History
# written then, or imported from a tracker on that schema, keys each comment by its own id there.
ISSUE_COMMENTS = 'issue_comments'


@dataclass(frozen=True)
class Hanging:
    """A table whose rows hang off a parent, and the column in each row naming that parent."""

    table: str
    parent_column: str


KIND_MODELS: dict[Kind, type[models.Idea] | type[models.Initiative] | type[models.Issue]] = {
    Kind.IDEA: models.Idea,
    Kind.INITIATIVE: models.Initiative,
    Kind.ISSUE: models.Issue,
}

# Rows naming the record by its number, whatever its kind.
ON_THE_NUMBER = tuple(
    Hanging(model.__tablename__, 'record_number') for model in (models.Claim, models.Comment, models.Document, models.Worklog)
)

# Rows naming the record by its kind's own id. A dependency is the record's from either end.
ON_THE_ID: dict[Kind, tuple[Hanging, ...]] = {
    Kind.IDEA: (Hanging(models.IdeaEvidence.__tablename__, 'idea_id'),),
    Kind.INITIATIVE: (),
    Kind.ISSUE: (
        Hanging(models.IssueDependency.__tablename__, 'issue_id'),
        Hanging(models.IssueDependency.__tablename__, 'depends_on_id'),
        Hanging(models.IssueLabelAssignment.__tablename__, 'issue_id'),
        Hanging(ISSUE_COMMENTS, 'issue_id'),
    ),
}

# Rows naming a row found above by its id. A worklog's spans are left out: each is a session
# joining or leaving the worklog, which every hold and resume writes several of.
ON_A_ROW_FOUND = {
    models.Claim.__tablename__: Hanging(models.ClaimWait.__tablename__, 'claim_id'),
    models.Document.__tablename__: Hanging(models.DocumentVersion.__tablename__, 'document_id'),
    models.Worklog.__tablename__: Hanging(models.WorklogLink.__tablename__, 'worklog_id'),
}


def record_history(session: Session, record: models.Record, limit: int | None) -> list[schemas.RecordChange]:
    """Every write to `record` and to each row hanging off it, newest first, the latest `limit` of them."""
    kind_model = KIND_MODELS[record.kind]
    record_id = session.scalars(select(kind_model.id).where(kind_model.number == record.number)).one()
    written: dict[str, set[str]] = {kind_model.__tablename__: {str(record_id)}, models.Record.__tablename__: {str(record.number)}}

    hanging = rows_naming(
        session,
        [(link, record.number) for link in ON_THE_NUMBER] + [(link, str(record_id)) for link in ON_THE_ID[record.kind]],
    )
    # These parents' ids are integers, which a row naming one holds as a JSON number.
    beneath = rows_naming(session, [(ON_A_ROW_FOUND[table], int(key)) for table, key in hanging if table in ON_A_ROW_FOUND])
    for table, key in hanging + beneath:
        written.setdefault(table, set()).add(key)

    query = (
        select(Change)
        .where(or_(*(and_(Change.table_name == table, Change.record_id.in_(sorted(keys))) for table, keys in written.items())))
        .order_by(Change.changed_ts.desc(), Change.id.desc())
    )
    return [schemas.RecordChange.model_validate(row) for row in session.scalars(apply_row_limit(query, limit))]


def rows_naming(session: Session, parents: list[tuple[Hanging, int | str]]) -> list[tuple[str, str]]:
    """The table and key of each row whose insert or delete names one of `parents`.

    Each parent is tested once over every table naming it in the same column. Postgres plans one
    index scan per test, so a test per table priced the index above reading the whole trail.
    """
    if not parents:
        return []
    tables_naming: dict[tuple[str, int | str], list[str]] = {}
    for link, key in parents:
        tables_naming.setdefault((link.parent_column, key), []).append(link.table)
    named = (
        and_(
            or_(Change.changes.contains({column: {'new': key}}), Change.changes.contains({column: {'old': key}})),
            Change.table_name.in_(tables),
        )
        for (column, key), tables in tables_naming.items()
    )
    found = session.execute(select(Change.table_name, Change.record_id).distinct().where(Change.operation != 'update', or_(*named)))
    return [(table, key) for table, key in found]
