"""a direction is a record kind ideas and initiatives join

Revision ID: 274fc2e5583a
Revises: 82f5faf504da
Create Date: 2026-11-10 07:40:42.154338

"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = '274fc2e5583a'
down_revision = '82f5faf504da'
branch_labels = None
depends_on = None

AUDITED = ('direction_statuses', 'directions', 'record_directions')


def upgrade() -> None:
    op.create_table(
        'direction_statuses',
        sa.Column('name', sa.Text(), nullable=False),
        sa.PrimaryKeyConstraint('name', name=op.f('pk_direction_statuses')),
    )
    op.create_table(
        'directions',
        sa.Column('id', sa.Uuid(), nullable=False),
        sa.Column('name', sa.Text(), nullable=False),
        sa.Column('summary', sa.Text(), nullable=False),
        sa.Column('description', sa.Text(), nullable=True),
        sa.Column('status', sa.Text(), server_default='active', nullable=False),
        sa.Column('status_reason', sa.Text(), nullable=True),
        sa.Column('created_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.Column('closed_ts', sa.DateTime(timezone=True), nullable=True),
        sa.Column('number', sa.BigInteger(), nullable=False),
        sa.Column('record_kind', sa.Text(), server_default='direction', nullable=False),
        sa.CheckConstraint("record_kind = 'direction'", name=op.f('ck_directions_record_kind_is_declared')),
        sa.CheckConstraint(
            "status <> 'dropped' OR status_reason IS NOT NULL", name=op.f('ck_directions_direction_dropped_requires_reason')
        ),
        sa.ForeignKeyConstraint(
            ['number', 'record_kind'], ['records.number', 'records.kind'], name=op.f('fk_directions_number_records'), ondelete='CASCADE'
        ),
        sa.ForeignKeyConstraint(['status'], ['direction_statuses.name'], name=op.f('fk_directions_status_direction_statuses')),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_directions')),
        sa.UniqueConstraint('number', name=op.f('uq_directions_number')),
    )
    op.create_index('uq_directions_name_active', 'directions', ['name'], unique=True, postgresql_where=sa.text("status = 'active'"))
    op.create_table(
        'record_directions',
        sa.Column('record_number', sa.BigInteger(), nullable=False),
        sa.Column('direction_id', sa.Uuid(), nullable=False),
        sa.Column('created_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.ForeignKeyConstraint(
            ['direction_id'], ['directions.id'], name=op.f('fk_record_directions_direction_id_directions'), ondelete='CASCADE'
        ),
        sa.ForeignKeyConstraint(
            ['record_number'], ['records.number'], name=op.f('fk_record_directions_record_number_records'), ondelete='CASCADE'
        ),
        sa.PrimaryKeyConstraint('record_number', 'direction_id', name=op.f('pk_record_directions')),
    )
    op.create_index('idx_record_directions_direction', 'record_directions', ['direction_id'], unique=False)

    op.execute("INSERT INTO record_kinds (name) VALUES ('direction')")
    op.execute("INSERT INTO direction_statuses (name) VALUES ('active'), ('someday'), ('completed'), ('dropped')")
    for table in AUDITED:
        op.execute(
            f'CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON "{table}" FOR EACH ROW EXECUTE FUNCTION audit_trail_record()'
        )


def downgrade() -> None:
    op.execute("DELETE FROM records WHERE kind = 'direction'")
    for table in reversed(AUDITED):
        op.execute(f'DROP TRIGGER audit_trail ON "{table}"')
    op.drop_index('idx_record_directions_direction', table_name='record_directions')
    op.drop_table('record_directions')
    op.drop_index('uq_directions_name_active', table_name='directions', postgresql_where=sa.text("status = 'active'"))
    op.drop_table('directions')
    op.drop_table('direction_statuses')
    op.execute("DELETE FROM record_kinds WHERE name = 'direction'")
