"""read each record's history by the rows its changes name

Revision ID: e2eebda09b7b
Revises: 3e89a5e22b1e
Create Date: 2026-11-02 11:09:06.833745

"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = 'e2eebda09b7b'
down_revision = '3e89a5e22b1e'
branch_labels = None
depends_on = None


def upgrade() -> None:
    # A rename keeps every stored moment, where autogenerate's drop and add would lose them. The
    # index on (table_name, record_id, changed_ts) follows the column by itself.
    op.alter_column('audit_trail', 'changed_at', new_column_name='changed_ts')
    op.execute('ALTER INDEX ix_audit_trail_changed_at RENAME TO ix_audit_trail_changed_ts')
    op.create_index(
        'idx_audit_trail_whole_rows',
        'audit_trail',
        ['changes'],
        unique=False,
        postgresql_using='gin',
        postgresql_ops={'changes': 'jsonb_path_ops'},
        postgresql_where=sa.text("operation <> 'update'"),
    )


def downgrade() -> None:
    op.drop_index('idx_audit_trail_whole_rows', table_name='audit_trail')
    op.execute('ALTER INDEX ix_audit_trail_changed_ts RENAME TO ix_audit_trail_changed_at')
    op.alter_column('audit_trail', 'changed_ts', new_column_name='changed_at')
