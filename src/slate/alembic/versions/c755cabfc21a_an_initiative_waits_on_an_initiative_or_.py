"""an initiative waits on an initiative or an issue

Revision ID: c755cabfc21a
Revises: f8ec31cbaaac
Create Date: 2026-11-09 14:06:51.662789

"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = 'c755cabfc21a'
down_revision = 'f8ec31cbaaac'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        'initiative_dependencies',
        sa.Column('initiative_id', sa.Uuid(), nullable=False),
        sa.Column('depends_on_number', sa.BigInteger(), nullable=False),
        sa.ForeignKeyConstraint(
            ['depends_on_number'], ['records.number'], name=op.f('fk_initiative_dependencies_depends_on_number_records'), ondelete='CASCADE'
        ),
        sa.ForeignKeyConstraint(
            ['initiative_id'], ['initiatives.id'], name=op.f('fk_initiative_dependencies_initiative_id_initiatives'), ondelete='CASCADE'
        ),
        sa.PrimaryKeyConstraint('initiative_id', 'depends_on_number', name=op.f('pk_initiative_dependencies')),
    )
    op.create_index('idx_initiative_dependencies_depends_on', 'initiative_dependencies', ['depends_on_number'], unique=False)
    op.execute(
        'CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON "initiative_dependencies" '
        'FOR EACH ROW EXECUTE FUNCTION audit_trail_record()'
    )


def downgrade() -> None:
    op.execute('DROP TRIGGER audit_trail ON "initiative_dependencies"')
    op.drop_index('idx_initiative_dependencies_depends_on', table_name='initiative_dependencies')
    op.drop_table('initiative_dependencies')
