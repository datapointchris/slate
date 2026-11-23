"""drop idea_evidence, which the release before reads no more

Revision ID: eccf77f525dc
Revises: 9939558ba662
Create Date: 2026-11-23 02:24:55.177146

The release before this one reads and writes an idea's refs only in `record_refs`, so
`idea_evidence` goes, with the two triggers that kept it in step. The trigger on `record_refs` is
dropped first, which waits out every link in flight, so no write forwards into a table already gone.
Dropping a table drops its triggers but leaves their functions, so both are dropped by name. The
audit trail keeps every write the table took under its name, and `services/record_changes.py`
reads them there.

The downgrade rebuilds the table as the revision before left it, each idea's refs copied in. The
copy runs before its audit trigger exists, so it writes no audit rows, and before the forwarding
triggers exist, so nothing is copied back.
"""

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

# revision identifiers, used by Alembic.
revision = 'eccf77f525dc'
down_revision = '9939558ba662'
branch_labels = None
depends_on = None

COPY_IDEA_REFS_TO_EVIDENCE = """
INSERT INTO idea_evidence (idea_id, ref, note, linked_by, linked_ts)
SELECT ideas.id, record_refs.ref, record_refs.note, record_refs.linked_by, record_refs.linked_ts
FROM record_refs JOIN ideas ON ideas.number = record_refs.record_number
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

FORWARD_IDEA_REFS_TO_EVIDENCE = """
CREATE FUNCTION forward_idea_refs_to_idea_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO idea_evidence (idea_id, ref, note, linked_by, linked_ts)
    SELECT ideas.id, NEW.ref, NEW.note, NEW.linked_by, NEW.linked_ts FROM ideas WHERE ideas.number = NEW.record_number
    ON CONFLICT DO NOTHING;
    RETURN NULL;
END;
$$;
CREATE TRIGGER forward_to_idea_evidence AFTER INSERT ON record_refs
    FOR EACH ROW EXECUTE FUNCTION forward_idea_refs_to_idea_evidence();
"""


def upgrade() -> None:
    op.execute('DROP TRIGGER forward_to_idea_evidence ON record_refs')
    op.execute('DROP FUNCTION forward_idea_refs_to_idea_evidence()')
    op.drop_table('idea_evidence')
    op.execute('DROP FUNCTION forward_idea_evidence_writes_to_record_refs()')
    op.execute("DELETE FROM audit_trail_tables WHERE table_schema = 'public' AND table_name = 'idea_evidence'")


def downgrade() -> None:
    op.create_table(
        'idea_evidence',
        sa.Column('idea_id', sa.UUID(), autoincrement=False, nullable=False),
        sa.Column('ref', sa.TEXT(), autoincrement=False, nullable=False),
        sa.Column('note', sa.TEXT(), autoincrement=False, nullable=True),
        sa.Column('linked_by', sa.TEXT(), autoincrement=False, nullable=True),
        sa.Column('linked_ts', postgresql.TIMESTAMP(timezone=True), server_default=sa.text('now()'), autoincrement=False, nullable=False),
        sa.CheckConstraint("btrim(ref) <> ''::text", name=op.f('ck_idea_evidence_ref_not_blank')),
        sa.ForeignKeyConstraint(['idea_id'], ['ideas.id'], name=op.f('fk_idea_evidence_idea_id_ideas'), ondelete='CASCADE'),
        sa.PrimaryKeyConstraint('idea_id', 'ref', name=op.f('pk_idea_evidence')),
    )
    op.execute(COPY_IDEA_REFS_TO_EVIDENCE)
    op.execute(
        'CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON idea_evidence FOR EACH ROW EXECUTE FUNCTION audit_trail_record()'
    )
    op.execute(FORWARD_EVIDENCE_WRITES)
    op.execute(FORWARD_IDEA_REFS_TO_EVIDENCE)
