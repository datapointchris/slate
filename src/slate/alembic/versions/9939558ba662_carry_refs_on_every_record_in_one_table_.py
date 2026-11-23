"""carry refs on every record in one table, and an idea's evidence across

Revision ID: 9939558ba662
Revises: 34d106f9667d
Create Date: 2026-11-22 20:36:01.169192

Every idea's evidence becomes a row of `record_refs` under the idea's number, with its note,
linker and time. The release before refs reads and writes `idea_evidence` while both serve, so it
stays until a later migration drops it. A trigger on it forwards each row that the release before
refs links, so the new release reads every link either one takes. The release before refs only
removes evidence by deleting its idea, which deletes the idea's refs through `records`, so no
delete is forwarded. The copy runs before the audit trigger on `record_refs` exists, so it writes
no audit rows.
"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = '9939558ba662'
down_revision = '34d106f9667d'
branch_labels = None
depends_on = None

COPY_EVIDENCE_TO_REFS = """
INSERT INTO record_refs (record_number, ref, note, linked_by, linked_ts)
SELECT ideas.number, idea_evidence.ref, idea_evidence.note, idea_evidence.linked_by, idea_evidence.linked_ts
FROM idea_evidence JOIN ideas ON ideas.id = idea_evidence.idea_id
"""

FORWARD_EVIDENCE_WRITES = """
CREATE FUNCTION forward_idea_evidence_writes_to_record_refs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO record_refs (record_number, ref, note, linked_by, linked_ts)
    SELECT ideas.number, NEW.ref, NEW.note, NEW.linked_by, NEW.linked_ts FROM ideas WHERE ideas.id = NEW.idea_id
    ON CONFLICT DO NOTHING;
    RETURN NULL;
END;
$$;
CREATE TRIGGER forward_to_record_refs AFTER INSERT ON idea_evidence
    FOR EACH ROW EXECUTE FUNCTION forward_idea_evidence_writes_to_record_refs();
"""

COPY_IDEA_REFS_TO_EVIDENCE = """
INSERT INTO idea_evidence (idea_id, ref, note, linked_by, linked_ts)
SELECT ideas.id, record_refs.ref, record_refs.note, record_refs.linked_by, record_refs.linked_ts
FROM record_refs JOIN ideas ON ideas.number = record_refs.record_number
"""


def upgrade() -> None:
    op.create_table(
        'record_refs',
        sa.Column('record_number', sa.BigInteger(), nullable=False),
        sa.Column('ref', sa.Text(), nullable=False),
        sa.Column('note', sa.Text(), nullable=True),
        sa.Column('linked_by', sa.Text(), nullable=True),
        sa.Column('linked_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.CheckConstraint("btrim(ref) <> ''", name=op.f('ck_record_refs_ref_not_blank')),
        sa.ForeignKeyConstraint(
            ['record_number'], ['records.number'], name=op.f('fk_record_refs_record_number_records'), ondelete='CASCADE'
        ),
        sa.PrimaryKeyConstraint('record_number', 'ref', name=op.f('pk_record_refs')),
    )
    op.create_index('idx_record_refs_ref_prefix', 'record_refs', ['ref'], unique=False, postgresql_ops={'ref': 'text_pattern_ops'})
    op.execute(COPY_EVIDENCE_TO_REFS)
    op.execute(FORWARD_EVIDENCE_WRITES)


def downgrade() -> None:
    """Replace `idea_evidence`'s rows with every idea's refs, then drop `record_refs`. An issue's or an initiative's refs are lost.

    The forwarding put every row that the release before refs linked into `record_refs`, so the
    ideas' refs there hold all of it. The copy back writes no audit rows, as the copy forward wrote none.
    """
    op.execute('DROP TRIGGER forward_to_record_refs ON idea_evidence')
    op.execute('DROP FUNCTION forward_idea_evidence_writes_to_record_refs()')
    op.execute('ALTER TABLE idea_evidence DISABLE TRIGGER audit_trail')
    op.execute('DELETE FROM idea_evidence')
    op.execute(COPY_IDEA_REFS_TO_EVIDENCE)
    op.execute('ALTER TABLE idea_evidence ENABLE TRIGGER audit_trail')
    op.drop_index('idx_record_refs_ref_prefix', table_name='record_refs', postgresql_ops={'ref': 'text_pattern_ops'})
    op.drop_table('record_refs')
