"""number every record in a records table

Revision ID: cc0612b31feb
Revises: 2bfe3793ccbe
Create Date: 2026-11-02 04:52:44.165379

"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = 'cc0612b31feb'
down_revision = '2bfe3793ccbe'
branch_labels = None
depends_on = None

KINDS = (('issues', 'issue'), ('initiatives', 'initiative'))
AUDITED_TABLES = ('record_kinds', 'records')


def upgrade() -> None:
    op.execute('ALTER SEQUENCE item_numbers RENAME TO record_numbers')
    op.create_table(
        'record_kinds',
        sa.Column('name', sa.Text(), nullable=False),
        sa.PrimaryKeyConstraint('name', name=op.f('pk_record_kinds')),
    )
    op.create_table(
        'records',
        sa.Column('number', sa.BigInteger(), server_default=sa.text("nextval('record_numbers')"), nullable=False),
        sa.Column('kind', sa.Text(), nullable=False),
        sa.ForeignKeyConstraint(['kind'], ['record_kinds.name'], name=op.f('fk_records_kind_record_kinds')),
        sa.PrimaryKeyConstraint('number', name=op.f('pk_records')),
        sa.UniqueConstraint('number', 'kind', name='uq_records_number_kind'),
    )
    op.execute('ALTER SEQUENCE record_numbers OWNED BY records.number')
    op.execute("INSERT INTO record_kinds (name) VALUES ('idea'), ('initiative'), ('issue')")
    for table, kind in KINDS:
        op.execute(f"INSERT INTO records (number, kind) SELECT number, '{kind}' FROM {table}")
        op.alter_column(table, 'number', server_default=None)
        op.add_column(table, sa.Column('record_kind', sa.Text(), server_default=kind, nullable=False))
        op.create_check_constraint(op.f(f'ck_{table}_record_kind_is_declared'), table, f"record_kind = '{kind}'")
        op.create_foreign_key(
            op.f(f'fk_{table}_number_records'), table, 'records', ['number', 'record_kind'], ['number', 'kind'], ondelete='CASCADE'
        )
    for table in AUDITED_TABLES:
        op.execute(
            f'CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON "{table}" FOR EACH ROW EXECUTE FUNCTION audit_trail_record()'
        )


def downgrade() -> None:
    for table in AUDITED_TABLES:
        op.execute(f'DROP TRIGGER audit_trail ON "{table}"')
    for table, _kind in KINDS:
        op.drop_constraint(op.f(f'fk_{table}_number_records'), table, type_='foreignkey')
        op.drop_constraint(op.f(f'ck_{table}_record_kind_is_declared'), table, type_='check')
        op.drop_column(table, 'record_kind')
    op.execute('ALTER SEQUENCE record_numbers OWNED BY NONE')
    op.drop_table('records')
    op.drop_table('record_kinds')
    op.execute('ALTER SEQUENCE record_numbers RENAME TO item_numbers')
    for table, _kind in KINDS:
        op.alter_column(table, 'number', server_default=sa.text("nextval('item_numbers')"))
