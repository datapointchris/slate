"""ideas, initiatives and issues carry tags from a closed list

Revision ID: 82f5faf504da
Revises: 525e28c1482c
Create Date: 2026-11-10 06:46:16.030685

"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = '82f5faf504da'
down_revision = '525e28c1482c'
branch_labels = None
depends_on = None

AUDITED = ('tags', 'record_tags')


def upgrade() -> None:
    op.create_table(
        'tags',
        sa.Column('id', sa.Integer(), sa.Identity(always=True), nullable=False),
        sa.Column('name', sa.Text(), nullable=False),
        sa.Column('description', sa.Text(), nullable=False),
        sa.Column('created_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.CheckConstraint("name ~ '^[a-z0-9]+(-[a-z0-9]+)*$'", name=op.f('ck_tags_tag_name_shape')),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_tags')),
        sa.UniqueConstraint('name', name=op.f('uq_tags_name')),
    )
    op.create_table(
        'record_tags',
        sa.Column('record_number', sa.BigInteger(), nullable=False),
        sa.Column('tag_id', sa.Integer(), nullable=False),
        sa.Column('created_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.ForeignKeyConstraint(
            ['record_number'], ['records.number'], name=op.f('fk_record_tags_record_number_records'), ondelete='CASCADE'
        ),
        sa.ForeignKeyConstraint(['tag_id'], ['tags.id'], name=op.f('fk_record_tags_tag_id_tags'), ondelete='CASCADE'),
        sa.PrimaryKeyConstraint('record_number', 'tag_id', name=op.f('pk_record_tags')),
    )
    op.create_index('idx_record_tags_tag', 'record_tags', ['tag_id'], unique=False)
    for table in AUDITED:
        op.execute(
            f'CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON "{table}" FOR EACH ROW EXECUTE FUNCTION audit_trail_record()'
        )


def downgrade() -> None:
    for table in reversed(AUDITED):
        op.execute(f'DROP TRIGGER audit_trail ON "{table}"')
    op.drop_index('idx_record_tags_tag', table_name='record_tags')
    op.drop_table('record_tags')
    op.drop_table('tags')
