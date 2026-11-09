"""autofix is false by default

Revision ID: f8ec31cbaaac
Revises: 9913933845e9
Create Date: 2026-11-08 23:06:09.188021

"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = 'f8ec31cbaaac'
down_revision = '9913933845e9'
branch_labels = None
depends_on = None


def upgrade() -> None:
    # Autogenerate compares no server defaults, so this is written by hand. Rows keep their value.
    op.alter_column('issues', 'autofix', server_default=sa.text('false'))
    op.alter_column('initiatives', 'autofix', server_default=sa.text('false'))


def downgrade() -> None:
    op.alter_column('issues', 'autofix', server_default=sa.text('true'))
    op.alter_column('initiatives', 'autofix', server_default=sa.text('true'))
