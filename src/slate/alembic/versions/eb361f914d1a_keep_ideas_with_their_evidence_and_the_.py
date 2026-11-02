"""keep ideas with their evidence, and the initiative each becomes

Revision ID: eb361f914d1a
Revises: 33cdcacc4f17
Create Date: 2026-11-02 05:42:25.586451

"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = 'eb361f914d1a'
down_revision = '33cdcacc4f17'
branch_labels = None
depends_on = None

AUDITED_TABLES = ('ideas', 'idea_evidence')


def upgrade() -> None:
    op.create_table(
        'ideas',
        sa.Column('id', sa.Uuid(), nullable=False),
        sa.Column('title', sa.Text(), nullable=False),
        sa.Column('description', sa.Text(), nullable=True),
        sa.Column('priority', sa.SmallInteger(), server_default='0', nullable=False),
        sa.Column('created_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.Column('updated_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.Column('number', sa.BigInteger(), nullable=False),
        sa.Column('record_kind', sa.Text(), server_default='idea', nullable=False),
        sa.CheckConstraint("record_kind = 'idea'", name=op.f('ck_ideas_record_kind_is_declared')),
        sa.CheckConstraint('priority BETWEEN 0 AND 4', name=op.f('ck_ideas_idea_priority_range')),
        sa.ForeignKeyConstraint(
            ['number', 'record_kind'], ['records.number', 'records.kind'], name=op.f('fk_ideas_number_records'), ondelete='CASCADE'
        ),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_ideas')),
        sa.UniqueConstraint('number', name=op.f('uq_ideas_number')),
    )
    op.create_table(
        'idea_evidence',
        sa.Column('idea_id', sa.Uuid(), nullable=False),
        sa.Column('ref', sa.Text(), nullable=False),
        sa.Column('note', sa.Text(), nullable=True),
        sa.Column('linked_by', sa.Text(), nullable=True),
        sa.Column('linked_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.CheckConstraint("btrim(ref) <> ''", name=op.f('ck_idea_evidence_ref_not_blank')),
        sa.ForeignKeyConstraint(['idea_id'], ['ideas.id'], name=op.f('fk_idea_evidence_idea_id_ideas'), ondelete='CASCADE'),
        sa.PrimaryKeyConstraint('idea_id', 'ref', name=op.f('pk_idea_evidence')),
    )
    op.add_column('initiatives', sa.Column('idea_id', sa.Uuid(), nullable=True))
    op.create_unique_constraint(op.f('uq_initiatives_idea_id'), 'initiatives', ['idea_id'])
    op.create_foreign_key(op.f('fk_initiatives_idea_id_ideas'), 'initiatives', 'ideas', ['idea_id'], ['id'], ondelete='SET NULL')
    for table in AUDITED_TABLES:
        op.execute(
            f'CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON "{table}" FOR EACH ROW EXECUTE FUNCTION audit_trail_record()'
        )


def downgrade() -> None:
    for table in AUDITED_TABLES:
        op.execute(f'DROP TRIGGER audit_trail ON "{table}"')
    op.drop_constraint(op.f('fk_initiatives_idea_id_ideas'), 'initiatives', type_='foreignkey')
    op.drop_constraint(op.f('uq_initiatives_idea_id'), 'initiatives', type_='unique')
    op.drop_column('initiatives', 'idea_id')
    op.drop_table('idea_evidence')
    op.drop_table('ideas')
    op.execute("DELETE FROM records WHERE kind = 'idea'")
