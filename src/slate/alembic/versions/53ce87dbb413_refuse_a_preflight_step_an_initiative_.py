"""refuse a preflight step an initiative marks as not planned

Revision ID: 53ce87dbb413
Revises: 9fabf6cb9105
Create Date: 2026-11-14 13:15:31.319891

Work large enough to be an initiative needs a preflight, so the check refuses `preflight` beside
`build`. A database restored from a release that accepted a preflight mark may hold one, so any is
deleted before the check is added, and that initiative plans its preflight again. The release before
still reads and writes the table, and its write of a preflight mark is refused here.
"""

from alembic import op

# revision identifiers, used by Alembic.
revision = '53ce87dbb413'
down_revision = '9fabf6cb9105'
branch_labels = None
depends_on = None

TABLE = 'initiative_steps_not_planned'


def upgrade() -> None:
    op.execute(f"DELETE FROM {TABLE} WHERE step = 'preflight'")
    op.drop_constraint(op.f(f'ck_{TABLE}_build_always_planned'), TABLE, type_='check')
    op.create_check_constraint(op.f(f'ck_{TABLE}_never_build_or_preflight'), TABLE, "step NOT IN ('build', 'preflight')")


def downgrade() -> None:
    op.drop_constraint(op.f(f'ck_{TABLE}_never_build_or_preflight'), TABLE, type_='check')
    op.create_check_constraint(op.f(f'ck_{TABLE}_build_always_planned'), TABLE, "step <> 'build'")
