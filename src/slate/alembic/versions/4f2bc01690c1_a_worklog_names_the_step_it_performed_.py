"""a worklog names the step it performed, and a retro is a document kind

Revision ID: 4f2bc01690c1
Revises: c755cabfc21a
Create Date: 2026-11-10 04:40:46.282783

`worklogs.workflow` and `worklog_workflows` stay, because the API one release
behind still reads them.
"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = '4f2bc01690c1'
down_revision = 'c755cabfc21a'
branch_labels = None
depends_on = None

STEPS = (
    ('shape', "Talk an idea through and write the initiative's pitch: the problem, the appetite, what is out, and the rabbit holes", 1),
    ('design', 'Research the open questions, check the work against the system it joins, and write the design document', 2),
    ('preflight', 'Check what the plan will run against what is allowed, estimate its tokens by kind, and write the plan document', 3),
    ('build', "Build the plan's issues and close them", 4),
    ('retro', "Read the finished work's follow-up fixes, review findings and tokens against the estimate, and write the retro document", 5),
)


def upgrade() -> None:
    steps = op.create_table(
        'worklog_steps',
        sa.Column('name', sa.Text(), nullable=False),
        sa.Column('description', sa.Text(), nullable=False),
        sa.Column('position', sa.Integer(), nullable=False),
        sa.PrimaryKeyConstraint('name', name=op.f('pk_worklog_steps')),
        sa.UniqueConstraint('position', name=op.f('uq_worklog_steps_position')),
    )
    op.add_column('worklogs', sa.Column('step', sa.Text(), nullable=True))
    op.create_foreign_key(op.f('fk_worklogs_step_worklog_steps'), 'worklogs', 'worklog_steps', ['step'], ['name'])
    op.execute(
        'CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON "worklog_steps" FOR EACH ROW EXECUTE FUNCTION audit_trail_record()'
    )
    op.bulk_insert(steps, [{'name': name, 'description': description, 'position': position} for name, description, position in STEPS])
    op.execute("INSERT INTO document_kinds (name) VALUES ('retro')")


def downgrade() -> None:
    op.execute("DELETE FROM document_kinds WHERE name = 'retro'")
    op.drop_constraint(op.f('fk_worklogs_step_worklog_steps'), 'worklogs', type_='foreignkey')
    op.drop_column('worklogs', 'step')
    op.execute('DROP TRIGGER audit_trail ON "worklog_steps"')
    op.drop_table('worklog_steps')
