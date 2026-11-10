"""a pursuit is the record kind directions were, and carries its measure

Revision ID: bd10139daa32
Revises: 274fc2e5583a
Create Date: 2026-11-10 09:40:16.181981

Every direction becomes a pursuit under its number, its id, and every idea's and initiative's
placement in it. The release before pursuits reads and writes `directions`, `direction_statuses`
and `record_directions` while both serve, so they stay until a later migration drops them. Each
number's kind becomes `pursuit`, so `directions` loses its key onto `records`, and `record_kinds`
keeps `direction` for the row the release before inserts ahead of each direction it creates.

A trigger on each old table forwards what the release before writes there: a direction it creates
or edits, and a placement it adds or removes. So the new release reads every write either one
takes while both serve. The copy runs before the audit triggers on the new tables exist, so it
writes no audit rows.
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
    op.execute(FORWARD_DIRECTION_WRITES)
    op.execute(FORWARD_PLACEMENT_WRITES)


def downgrade() -> None:
    """Replace the direction tables' rows with every pursuit and placement, then drop the pursuit tables. Each `measure` is lost.

    The forwarding put every direction into `pursuits` under its id, so the pursuit tables hold all
    of it. Emptying the direction tables first leaves no older row whose active name a pursuit's
    could clash with. The copy back writes no audit rows, as the copy forward wrote none.
    """
    op.execute('DROP TRIGGER forward_to_record_pursuits ON record_directions')
    op.execute('DROP FUNCTION forward_record_direction_writes_to_record_pursuits()')
    op.execute('DROP TRIGGER forward_to_pursuits ON directions')
    op.execute('DROP FUNCTION forward_direction_writes_to_pursuits()')
    op.execute('ALTER TABLE record_directions DISABLE TRIGGER audit_trail')
    op.execute('ALTER TABLE directions DISABLE TRIGGER audit_trail')
    op.execute('DELETE FROM directions')
    op.execute(f'INSERT INTO directions ({COPIED_COLUMNS}) SELECT {COPIED_COLUMNS} FROM pursuits')
    op.execute(
        'INSERT INTO record_directions (record_number, direction_id, created_ts)'
        ' SELECT record_number, pursuit_id, created_ts FROM record_pursuits'
    )
    op.execute('ALTER TABLE directions ENABLE TRIGGER audit_trail')
    op.execute('ALTER TABLE record_directions ENABLE TRIGGER audit_trail')
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
