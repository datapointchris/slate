"""Every write to a slate table leaves a row in `audit_trail`, naming who made it.

A row trigger on each table writes the entry, so every path that reaches the database is audited:
the ORM, a Core statement, raw SQL, and an `ON DELETE` action Postgres takes itself. The trigger
reads who is writing from three transaction-local settings, which `name_the_writer` sets each time
a session begins a transaction. The API binds the caller's actor to the session where it resolves
the caller: a person's username, else their `sub`, or a service client's id. `request_id` and `job_run_id` come from structlog's context,
where the request middleware or a batch job binds them.

A table opts out with `'info': {'record_changes': False}` in its `__table_args__`, and a column
with the same key in its `info`. Each declaration is mirrored in the table's trigger, which the
audit trail's tests compare against the models.
"""

import datetime as dt
from typing import Any

import structlog
from sqlalchemy import BigInteger
from sqlalchemy import CheckConstraint
from sqlalchemy import Connection
from sqlalchemy import DateTime
from sqlalchemy import Identity
from sqlalchemy import Index
from sqlalchemy import Text
from sqlalchemy import event
from sqlalchemy import func
from sqlalchemy import select
from sqlalchemy import text
from sqlalchemy.dialects.postgresql import JSONB
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import Session
from sqlalchemy.orm import SessionTransaction
from sqlalchemy.orm import mapped_column

from slate.database.base import Base

RECORDED = 'record_changes'
ACTOR = 'record_changes_actor'


class RecordChange(Base):
    """One write to one row of any table, and who made it.

    `changes` maps each column the write touched to `{"old": ..., "new": ...}`. An insert carries no
    `old`, and a delete no `new`. So an insert or a delete holds the whole row, and only those name
    the row's parent: a claim's `record_number`, a version's `document_id`.
    """

    __tablename__ = 'audit_trail'
    __table_args__ = (
        CheckConstraint("operation IN ('insert', 'update', 'delete')", name='operation_is_a_write'),
        Index('idx_audit_trail_record', 'table_name', 'record_id', 'changed_ts'),
        # Finds the rows naming a parent, through `changes @> {"<column>": {"new": <key>}}`.
        Index(
            'idx_audit_trail_whole_rows',
            'changes',
            postgresql_using='gin',
            postgresql_ops={'changes': 'jsonb_path_ops'},
            postgresql_where=text("operation <> 'update'"),
        ),
        {'info': {RECORDED: False}},
    )

    id: Mapped[int] = mapped_column(BigInteger, Identity(always=True), primary_key=True)
    changed_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), server_default=func.now(), nullable=False, index=True)
    table_name: Mapped[str] = mapped_column(Text, nullable=False)
    record_id: Mapped[str] = mapped_column(Text, nullable=False)
    operation: Mapped[str] = mapped_column(Text, nullable=False)
    changes: Mapped[dict[str, Any]] = mapped_column(JSONB, nullable=False)
    actor: Mapped[str | None] = mapped_column(Text, nullable=True, index=True)
    request_id: Mapped[str | None] = mapped_column(Text, nullable=True)
    job_run_id: Mapped[str | None] = mapped_column(Text, nullable=True)

    def __repr__(self) -> str:
        return f'RecordChange({self.operation} {self.table_name}/{self.record_id} by {self.actor!r})'


def bind_actor(session: Session, actor: str) -> None:
    """Name who makes every write `session` sends from here on, including in the open transaction."""
    session.info[ACTOR] = actor
    if session.in_transaction():
        set_writer_settings(session, session.connection())


def set_writer_settings(session: Session, connection: Connection) -> None:
    bound = structlog.contextvars.get_contextvars()
    connection.execute(
        select(
            func.set_config('audit.actor', session.info.get(ACTOR) or '', True),
            func.set_config('audit.request_id', bound.get('request_id') or '', True),
            func.set_config('audit.job_run_id', bound.get('job_run_id') or '', True),
        )
    )


@event.listens_for(Session, 'after_begin')
def name_the_writer(session: Session, _transaction: SessionTransaction, connection: Connection) -> None:
    """Tell the audit trigger who writes in this transaction. The settings end with it."""
    set_writer_settings(session, connection)
