"""The table that held an idea's refs as evidence, which nothing here reads or writes.

The release before refs reads and writes it, and it serves beside this one through a rollout, so
it stays until a later migration drops it. It is declared so `alembic check` finds the database as
the models say. A trigger on it forwards each row that the release before refs links into
`record_refs`. Its key onto `ideas` cascades, so the writes made to it read as the idea's own in
`services/record_changes.py`.
"""

from __future__ import annotations

from sqlalchemy import CheckConstraint
from sqlalchemy import Column
from sqlalchemy import DateTime
from sqlalchemy import ForeignKey
from sqlalchemy import Table
from sqlalchemy import Text
from sqlalchemy import Uuid
from sqlalchemy import text

from slate.database.base import Base

idea_evidence = Table(
    'idea_evidence',
    Base.metadata,
    Column('idea_id', Uuid, ForeignKey('ideas.id', ondelete='CASCADE'), primary_key=True),
    Column('ref', Text, primary_key=True),
    Column('note', Text, nullable=True),
    Column('linked_by', Text, nullable=True),
    Column('linked_ts', DateTime(timezone=True), nullable=False, server_default=text('now()')),
    CheckConstraint("btrim(ref) <> ''", name='ref_not_blank'),
)
