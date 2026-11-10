"""drop the direction tables the release before pursuits wrote

Revision ID: 9e8d02b5c6d5
Revises: bd10139daa32
Create Date: 2026-11-10 10:59:04.643352

The release before this one reads and writes only `pursuits`, `pursuit_statuses` and
`record_pursuits`, so `directions`, `direction_statuses` and `record_directions` go, with the
`direction` record kind. Dropping a table drops its triggers but leaves their functions, so the two
that forwarded writes into the pursuit tables are dropped by name. The audit trail keeps every
write the old tables took under their names, and `services/record_changes.py` reads them there.

The downgrade rebuilds the three tables as the revision before left them, each pursuit and
placement copied in under its id. The copy runs before their audit triggers exist, so it writes no
audit rows, and before the forwarding triggers exist, so nothing is copied back.
"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = '9e8d02b5c6d5'
down_revision = 'bd10139daa32'
branch_labels = None
depends_on = None

AUDITED = ('direction_statuses', 'directions', 'record_directions')
COPIED_COLUMNS = 'id, number, name, summary, description, status, status_reason, created_ts, closed_ts'

FORWARD_DIRECTION_WRITES = """
CREATE FUNCTION forward_direction_writes_to_pursuits() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE records SET kind = 'pursuit' WHERE number = NEW.number AND kind = 'direction';
        INSERT INTO pursuits (id, number, name, summary, description, status, status_reason, created_ts, closed_ts)
        VALUES (NEW.id, NEW.number, NEW.name, NEW.summary, NEW.description, NEW.status, NEW.status_reason, NEW.created_ts, NEW.closed_ts);
    ELSE
        -- Only what this write changed, so an edit the new release made to another field stands.
        UPDATE pursuits SET
            name = CASE WHEN NEW.name IS DISTINCT FROM OLD.name THEN NEW.name ELSE name END,
            summary = CASE WHEN NEW.summary IS DISTINCT FROM OLD.summary THEN NEW.summary ELSE summary END,
            description = CASE WHEN NEW.description IS DISTINCT FROM OLD.description THEN NEW.description ELSE description END,
            status = CASE WHEN NEW.status IS DISTINCT FROM OLD.status THEN NEW.status ELSE status END,
            status_reason = CASE WHEN NEW.status_reason IS DISTINCT FROM OLD.status_reason THEN NEW.status_reason ELSE status_reason END,
            closed_ts = CASE WHEN NEW.closed_ts IS DISTINCT FROM OLD.closed_ts THEN NEW.closed_ts ELSE closed_ts END
        WHERE id = NEW.id;
    END IF;
    RETURN NULL;
END;
$$;
CREATE TRIGGER forward_to_pursuits AFTER INSERT OR UPDATE ON directions
    FOR EACH ROW EXECUTE FUNCTION forward_direction_writes_to_pursuits();
"""

FORWARD_PLACEMENT_WRITES = """
CREATE FUNCTION forward_record_direction_writes_to_record_pursuits() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO record_pursuits (record_number, pursuit_id, created_ts)
        VALUES (NEW.record_number, NEW.direction_id, NEW.created_ts) ON CONFLICT DO NOTHING;
    ELSE
        DELETE FROM record_pursuits WHERE record_number = OLD.record_number AND pursuit_id = OLD.direction_id;
    END IF;
    RETURN NULL;
END;
$$;
CREATE TRIGGER forward_to_record_pursuits AFTER INSERT OR DELETE ON record_directions
    FOR EACH ROW EXECUTE FUNCTION forward_record_direction_writes_to_record_pursuits();
"""


def upgrade() -> None:
    op.drop_table('record_directions')
    op.drop_table('directions')
    op.drop_table('direction_statuses')
    op.execute('DROP FUNCTION forward_direction_writes_to_pursuits()')
    op.execute('DROP FUNCTION forward_record_direction_writes_to_record_pursuits()')
    op.execute("DELETE FROM record_kinds WHERE name = 'direction'")


def downgrade() -> None:
    op.execute("INSERT INTO record_kinds (name) VALUES ('direction')")
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

    op.execute('INSERT INTO direction_statuses (name) SELECT name FROM pursuit_statuses')
    op.execute(f'INSERT INTO directions ({COPIED_COLUMNS}) SELECT {COPIED_COLUMNS} FROM pursuits')
    op.execute(
        'INSERT INTO record_directions (record_number, direction_id, created_ts)'
        ' SELECT record_number, pursuit_id, created_ts FROM record_pursuits'
    )
    for table in AUDITED:
        op.execute(
            f'CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON "{table}" FOR EACH ROW EXECUTE FUNCTION audit_trail_record()'
        )
    op.execute(FORWARD_DIRECTION_WRITES)
    op.execute(FORWARD_PLACEMENT_WRITES)
