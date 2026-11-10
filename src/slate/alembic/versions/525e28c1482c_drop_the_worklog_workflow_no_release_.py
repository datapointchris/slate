"""drop the worklog workflow no release reads

Revision ID: 525e28c1482c
Revises: 1148973d82bf
Create Date: 2026-11-10 06:08:44.003618

`worklogs.workflow` and `worklog_workflows` were kept while a release mapping the column served. The
release before this one maps neither, so no statement it sends names them. Dropping the table drops
its audit trigger with it.

The downgrade puts the table back with its three rows and its trigger, and the column empty.
"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = '525e28c1482c'
down_revision = '1148973d82bf'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.drop_constraint(op.f('fk_worklogs_workflow_worklog_workflows'), 'worklogs', type_='foreignkey')
    op.drop_column('worklogs', 'workflow')
    op.drop_table('worklog_workflows')


def downgrade() -> None:
    op.create_table(
        'worklog_workflows',
        sa.Column('name', sa.Text(), nullable=False),
        sa.PrimaryKeyConstraint('name', name=op.f('pk_worklog_workflows')),
    )
    op.execute(
        'CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON "worklog_workflows"'
        ' FOR EACH ROW EXECUTE FUNCTION audit_trail_record()'
    )
    op.execute("INSERT INTO worklog_workflows (name) VALUES ('oneshot'), ('plan-first'), ('design-first')")
    op.add_column('worklogs', sa.Column('workflow', sa.Text(), nullable=True))
    op.create_foreign_key(op.f('fk_worklogs_workflow_worklog_workflows'), 'worklogs', 'worklog_workflows', ['workflow'], ['name'])
