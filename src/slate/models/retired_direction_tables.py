"""The tables pursuits were held in as directions, which nothing here reads or writes.

The release before pursuits reads and writes them, and it serves beside this one through a
rollout, so they stay until a later migration drops them. They are declared so `alembic check`
finds the database as the models say. `directions` lost its key onto `records` when each of its
numbers became a pursuit's, so the copy the release before reads is frozen at that migration.
"""

from __future__ import annotations

from sqlalchemy import BigInteger
from sqlalchemy import CheckConstraint
from sqlalchemy import Column
from sqlalchemy import DateTime
from sqlalchemy import ForeignKey
from sqlalchemy import Index
from sqlalchemy import Table
from sqlalchemy import Text
from sqlalchemy import Uuid
from sqlalchemy import text

from slate.database.base import Base
from slate.models.record_change import KNOWN_BY

# `record_kinds` keeps the kind's former name, so a direction the release before creates still finds its row.
RETIRED_RECORD_KIND = 'direction'

direction_statuses = Table('direction_statuses', Base.metadata, Column('name', Text, primary_key=True))

directions = Table(
    'directions',
    Base.metadata,
    Column('id', Uuid, primary_key=True),
    Column('name', Text, nullable=False),
    Column('summary', Text, nullable=False),
    Column('description', Text, nullable=True),
    Column('status', Text, ForeignKey('direction_statuses.name'), nullable=False, server_default='active'),
    Column('status_reason', Text, nullable=True),
    Column('created_ts', DateTime(timezone=True), nullable=False, server_default=text('now()')),
    Column('closed_ts', DateTime(timezone=True), nullable=True),
    Column('number', BigInteger, nullable=False, unique=True),
    Column('record_kind', Text, nullable=False, server_default='direction'),
    CheckConstraint("record_kind = 'direction'", name='record_kind_is_declared'),
    CheckConstraint("status <> 'dropped' OR status_reason IS NOT NULL", name='direction_dropped_requires_reason'),
    Index('uq_directions_name_active', 'name', unique=True, postgresql_where=text("status = 'active'")),
    info={KNOWN_BY: 'name'},
)

record_directions = Table(
    'record_directions',
    Base.metadata,
    Column('record_number', BigInteger, ForeignKey('records.number', ondelete='CASCADE'), primary_key=True),
    Column('direction_id', Uuid, ForeignKey('directions.id', ondelete='CASCADE'), primary_key=True),
    Column('created_ts', DateTime(timezone=True), nullable=False, server_default=text('now()')),
    Index('idx_record_directions_direction', 'direction_id'),
)
