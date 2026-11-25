"""drop initiatives.idea_id, which the release before reads no more

Revision ID: f721a977feb1
Revises: 86158442774b
Create Date: 2026-11-25 01:15:52.765746

Revision 86158442774b folded every idea that an initiative named here into that initiative, and
its check holds the column null since, so no row carries a value to lose. The release before
leaves the column out of its mapper, so it neither selects nor inserts it while this one rolls
out. The column goes with its unique constraint, its foreign key and that check.

The downgrade adds the column back empty, with all three.
"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = 'f721a977feb1'
down_revision = '86158442774b'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.drop_constraint(op.f('ck_initiatives_initiative_idea_id_unset'), 'initiatives', type_='check')
    op.drop_constraint(op.f('uq_initiatives_idea_id'), 'initiatives', type_='unique')
    op.drop_constraint(op.f('fk_initiatives_idea_id_ideas'), 'initiatives', type_='foreignkey')
    op.drop_column('initiatives', 'idea_id')


def downgrade() -> None:
    op.add_column('initiatives', sa.Column('idea_id', sa.UUID(), autoincrement=False, nullable=True))
    op.create_foreign_key(op.f('fk_initiatives_idea_id_ideas'), 'initiatives', 'ideas', ['idea_id'], ['id'], ondelete='SET NULL')
    op.create_unique_constraint(op.f('uq_initiatives_idea_id'), 'initiatives', ['idea_id'], postgresql_nulls_not_distinct=False)
    op.create_check_constraint(op.f('ck_initiatives_initiative_idea_id_unset'), 'initiatives', 'idea_id IS NULL')
