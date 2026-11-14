"""keep each step an initiative will not run

Revision ID: 9fabf6cb9105
Revises: 843f88f1fbd1
Create Date: 2026-11-14 12:10:34.407971

A new table, so the release before reads and writes nothing in it. A step with no row is planned,
so every initiative already stored keeps every step and nothing is backfilled. It is audited.
"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = '9fabf6cb9105'
down_revision = '843f88f1fbd1'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        'initiative_steps_not_planned',
        sa.Column('initiative_id', sa.Uuid(), nullable=False),
        sa.Column('step', sa.Text(), nullable=False),
        sa.Column('created_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.CheckConstraint("step <> 'build'", name=op.f('ck_initiative_steps_not_planned_build_always_planned')),
        sa.ForeignKeyConstraint(
            ['initiative_id'],
            ['initiatives.id'],
            name=op.f('fk_initiative_steps_not_planned_initiative_id_initiatives'),
            ondelete='CASCADE',
        ),
        sa.ForeignKeyConstraint(['step'], ['worklog_steps.name'], name=op.f('fk_initiative_steps_not_planned_step_worklog_steps')),
        sa.PrimaryKeyConstraint('initiative_id', 'step', name=op.f('pk_initiative_steps_not_planned')),
    )
    op.execute(
        'CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON "initiative_steps_not_planned" '
        'FOR EACH ROW EXECUTE FUNCTION audit_trail_record()'
    )


def downgrade() -> None:
    op.drop_table('initiative_steps_not_planned')
