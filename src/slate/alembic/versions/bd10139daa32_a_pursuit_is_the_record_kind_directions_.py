"""a pursuit is the record kind directions were, and carries its measure

Revision ID: bd10139daa32
Revises: 274fc2e5583a
Create Date: 2026-11-10 09:40:16.181981

Every direction becomes a pursuit under its number, its id, and every idea's and initiative's
placement in it. The release before reads and writes `directions`, `direction_statuses` and
`record_directions` while both serve, so they stay, holding the rows as they stood here, until a
later migration drops them. Each number's kind becomes `pursuit`, so `directions` loses its key
onto `records`, and `record_kinds` keeps `direction` for any row the release before still writes.

The copy runs before the new tables' audit triggers exist, so it writes no audit rows. Each
pursuit reads its writes as a direction through `record_changes_formerly` on `pursuits`.
"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = 'bd10139daa32'
down_revision = '274fc2e5583a'
branch_labels = None
depends_on = None

AUDITED = ('pursuit_statuses', 'pursuits', 'record_pursuits')
COPIED_COLUMNS = 'id, number, name, summary, description, status, status_reason, created_ts, closed_ts'


def upgrade() -> None:
    op.create_table(
        'pursuit_statuses',
        sa.Column('name', sa.Text(), nullable=False),
        sa.PrimaryKeyConstraint('name', name=op.f('pk_pursuit_statuses')),
    )
    op.create_table(
        'pursuits',
        sa.Column('id', sa.Uuid(), nullable=False),
        sa.Column('name', sa.Text(), nullable=False),
        sa.Column('summary', sa.Text(), nullable=False),
        sa.Column('description', sa.Text(), nullable=True),
        sa.Column('measure', sa.Text(), nullable=True),
        sa.Column('status', sa.Text(), server_default='active', nullable=False),
        sa.Column('status_reason', sa.Text(), nullable=True),
        sa.Column('created_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.Column('closed_ts', sa.DateTime(timezone=True), nullable=True),
        sa.Column('number', sa.BigInteger(), nullable=False),
        sa.Column('record_kind', sa.Text(), server_default='pursuit', nullable=False),
        sa.CheckConstraint("record_kind = 'pursuit'", name=op.f('ck_pursuits_record_kind_is_declared')),
        sa.CheckConstraint("status <> 'dropped' OR status_reason IS NOT NULL", name=op.f('ck_pursuits_pursuit_dropped_requires_reason')),
        sa.ForeignKeyConstraint(
            ['number', 'record_kind'], ['records.number', 'records.kind'], name=op.f('fk_pursuits_number_records'), ondelete='CASCADE'
        ),
        sa.ForeignKeyConstraint(['status'], ['pursuit_statuses.name'], name=op.f('fk_pursuits_status_pursuit_statuses')),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_pursuits')),
        sa.UniqueConstraint('number', name=op.f('uq_pursuits_number')),
    )
    op.create_index('uq_pursuits_name_active', 'pursuits', ['name'], unique=True, postgresql_where=sa.text("status = 'active'"))
    op.create_table(
        'record_pursuits',
        sa.Column('record_number', sa.BigInteger(), nullable=False),
        sa.Column('pursuit_id', sa.Uuid(), nullable=False),
        sa.Column('created_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.ForeignKeyConstraint(['pursuit_id'], ['pursuits.id'], name=op.f('fk_record_pursuits_pursuit_id_pursuits'), ondelete='CASCADE'),
        sa.ForeignKeyConstraint(
            ['record_number'], ['records.number'], name=op.f('fk_record_pursuits_record_number_records'), ondelete='CASCADE'
        ),
        sa.PrimaryKeyConstraint('record_number', 'pursuit_id', name=op.f('pk_record_pursuits')),
    )
    op.create_index('idx_record_pursuits_pursuit', 'record_pursuits', ['pursuit_id'], unique=False)

    op.execute("INSERT INTO record_kinds (name) VALUES ('pursuit')")
    op.execute('INSERT INTO pursuit_statuses (name) SELECT name FROM direction_statuses')
    op.drop_constraint(op.f('fk_directions_number_records'), 'directions', type_='foreignkey')
    op.execute("UPDATE records SET kind = 'pursuit' WHERE kind = 'direction'")
    op.execute(f'INSERT INTO pursuits ({COPIED_COLUMNS}) SELECT {COPIED_COLUMNS} FROM directions')
    op.execute(
        'INSERT INTO record_pursuits (record_number, pursuit_id, created_ts)'
        ' SELECT record_number, direction_id, created_ts FROM record_directions'
    )
    for table in AUDITED:
        op.execute(
            f'CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON "{table}" FOR EACH ROW EXECUTE FUNCTION audit_trail_record()'
        )


def downgrade() -> None:
    """Carry every pursuit and placement back into the direction tables, then drop the pursuit tables. Each `measure` is lost."""
    op.execute(
        f'INSERT INTO directions ({COPIED_COLUMNS}) SELECT {COPIED_COLUMNS} FROM pursuits ON CONFLICT (id) DO UPDATE SET'
        ' name = excluded.name, summary = excluded.summary, description = excluded.description, status = excluded.status,'
        ' status_reason = excluded.status_reason, closed_ts = excluded.closed_ts'
    )
    # A record deleted since the upgrade took its pursuit and placements with it, and its direction is left behind.
    op.execute('DELETE FROM directions WHERE number NOT IN (SELECT number FROM records)')
    op.execute(
        'DELETE FROM record_directions AS d WHERE d.direction_id IN (SELECT id FROM pursuits) AND NOT EXISTS'
        ' (SELECT FROM record_pursuits AS p WHERE p.record_number = d.record_number AND p.pursuit_id = d.direction_id)'
    )
    op.execute(
        'INSERT INTO record_directions (record_number, direction_id, created_ts)'
        ' SELECT record_number, pursuit_id, created_ts FROM record_pursuits ON CONFLICT DO NOTHING'
    )
    op.drop_index('idx_record_pursuits_pursuit', table_name='record_pursuits')
    op.drop_table('record_pursuits')
    op.drop_index('uq_pursuits_name_active', table_name='pursuits', postgresql_where=sa.text("status = 'active'"))
    op.drop_table('pursuits')
    op.drop_table('pursuit_statuses')
    op.execute("UPDATE records SET kind = 'direction' WHERE kind = 'pursuit'")
    op.create_foreign_key(
        op.f('fk_directions_number_records'), 'directions', 'records', ['number', 'record_kind'], ['number', 'kind'], ondelete='CASCADE'
    )
    op.execute("DELETE FROM record_kinds WHERE name = 'pursuit'")
