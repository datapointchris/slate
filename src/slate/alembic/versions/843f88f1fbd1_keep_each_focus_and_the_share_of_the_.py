"""keep each focus and the share of the work it names for each pursuit

Revision ID: 843f88f1fbd1
Revises: 9e8d02b5c6d5
Create Date: 2026-11-14 11:40:18.675822

Two new tables, so the release before reads and writes nothing in them. `focuses` holds each
focus, ended or not, and a partial unique index over a constant lets one row at a time leave
`ended_ts` null. `focus_shares` holds the share each focus names for a pursuit. Both are audited.
"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = '843f88f1fbd1'
down_revision = '9e8d02b5c6d5'
branch_labels = None
depends_on = None

AUDITED = ('focuses', 'focus_shares')


def upgrade() -> None:
    op.create_table(
        'focuses',
        sa.Column('id', sa.BigInteger(), sa.Identity(always=False), nullable=False),
        sa.Column('statement', sa.Text(), nullable=False),
        sa.Column('started_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.Column('until_date', sa.Date(), nullable=False),
        sa.Column('ended_ts', sa.DateTime(timezone=True), nullable=True),
        sa.Column('ended_reason', sa.Text(), nullable=True),
        sa.Column('set_by', sa.Text(), nullable=False),
        sa.CheckConstraint('(ended_ts IS NULL) = (ended_reason IS NULL)', name=op.f('ck_focuses_focus_ending_is_whole')),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_focuses')),
    )
    op.create_index(
        'uq_focuses_one_unended', 'focuses', [sa.literal_column('(true)')], unique=True, postgresql_where=sa.text('ended_ts IS NULL')
    )
    op.create_table(
        'focus_shares',
        sa.Column('focus_id', sa.BigInteger(), nullable=False),
        sa.Column('pursuit_id', sa.Uuid(), nullable=False),
        sa.Column('share', sa.Numeric(), nullable=False),
        sa.CheckConstraint('share >= 0 AND share <= 1', name=op.f('ck_focus_shares_focus_share_is_a_fraction')),
        sa.ForeignKeyConstraint(['focus_id'], ['focuses.id'], name=op.f('fk_focus_shares_focus_id_focuses'), ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['pursuit_id'], ['pursuits.id'], name=op.f('fk_focus_shares_pursuit_id_pursuits'), ondelete='CASCADE'),
        sa.PrimaryKeyConstraint('focus_id', 'pursuit_id', name=op.f('pk_focus_shares')),
    )
    op.create_index('idx_focus_shares_pursuit', 'focus_shares', ['pursuit_id'], unique=False)
    for table in AUDITED:
        op.execute(
            f'CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON "{table}" FOR EACH ROW EXECUTE FUNCTION audit_trail_record()'
        )


def downgrade() -> None:
    op.drop_index('idx_focus_shares_pursuit', table_name='focus_shares')
    op.drop_table('focus_shares')
    op.drop_index('uq_focuses_one_unended', table_name='focuses', postgresql_where=sa.text('ended_ts IS NULL'))
    op.drop_table('focuses')
