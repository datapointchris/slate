"""a worklog holds a token estimate by kind

Revision ID: 1148973d82bf
Revises: 4f2bc01690c1
Create Date: 2026-11-10 06:01:24.783643

Each column is null where that kind was not estimated, so an estimate of zero stays distinct from
none.
"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = '1148973d82bf'
down_revision = '4f2bc01690c1'
branch_labels = None
depends_on = None

ESTIMATED_KINDS = ('uncached_input', 'cache_write', 'cache_read', 'output')


def upgrade() -> None:
    for kind in ESTIMATED_KINDS:
        op.add_column('worklogs', sa.Column(f'estimated_{kind}_tokens', sa.BigInteger(), nullable=True))
    op.create_check_constraint(
        op.f('ck_worklogs_estimated_tokens_are_not_negative'),
        'worklogs',
        ' AND '.join(f'estimated_{kind}_tokens >= 0' for kind in ESTIMATED_KINDS),
    )


def downgrade() -> None:
    op.drop_constraint(op.f('ck_worklogs_estimated_tokens_are_not_negative'), 'worklogs', type_='check')
    for kind in reversed(ESTIMATED_KINDS):
        op.drop_column('worklogs', f'estimated_{kind}_tokens')
