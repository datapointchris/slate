"""keep every claim on a record in a claims table

Revision ID: 33cdcacc4f17
Revises: cc0612b31feb
Create Date: 2026-11-02 05:12:25.508377

An issue's claim moves off its row into `claims`, which any record kind's
claims share. An `in_progress` issue becomes `open`, and the claim it held
becomes an unended claim from its last update to its expiry.
"""

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

# revision identifiers, used by Alembic.
revision = '33cdcacc4f17'
down_revision = 'cc0612b31feb'
branch_labels = None
depends_on = None

AUDITED_TABLES = ('claim_endings', 'claims')


def upgrade() -> None:
    op.create_table(
        'claim_endings',
        sa.Column('name', sa.Text(), nullable=False),
        sa.PrimaryKeyConstraint('name', name=op.f('pk_claim_endings')),
    )
    op.create_table(
        'claims',
        sa.Column('id', sa.BigInteger(), sa.Identity(always=False), nullable=False),
        sa.Column('record_number', sa.BigInteger(), nullable=False),
        sa.Column('holder', sa.Text(), nullable=False),
        sa.Column('session', sa.Text(), nullable=True),
        sa.Column('host', sa.Text(), nullable=True),
        sa.Column('directory', sa.Text(), nullable=True),
        sa.Column('started_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.Column('expires_ts', sa.DateTime(timezone=True), nullable=False),
        sa.Column('ended_ts', sa.DateTime(timezone=True), nullable=True),
        sa.Column('ending', sa.Text(), nullable=True),
        sa.Column('ended_by', sa.Text(), nullable=True),
        sa.CheckConstraint('(ended_ts IS NULL) = (ending IS NULL)', name=op.f('ck_claims_claim_ending_is_whole')),
        sa.CheckConstraint('ended_by IS NULL OR ended_ts IS NOT NULL', name=op.f('ck_claims_claim_ended_by_only_when_ended')),
        sa.ForeignKeyConstraint(['ending'], ['claim_endings.name'], name=op.f('fk_claims_ending_claim_endings')),
        sa.ForeignKeyConstraint(['record_number'], ['records.number'], name=op.f('fk_claims_record_number_records'), ondelete='CASCADE'),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_claims')),
    )
    op.create_index('idx_claims_record_number', 'claims', ['record_number'], unique=False)
    op.create_index(op.f('ix_claims_holder'), 'claims', ['holder'], unique=False)
    op.create_index(
        'uq_claims_one_unended_per_record', 'claims', ['record_number'], unique=True, postgresql_where=sa.text('ended_ts IS NULL')
    )
    op.execute("INSERT INTO claim_endings (name) VALUES ('released'), ('expired'), ('completed'), ('canceled'), ('dropped')")
    op.execute(
        'INSERT INTO claims (record_number, holder, started_ts, expires_ts) '
        'SELECT number, claimed_by, LEAST(updated_ts, claim_expires_ts), claim_expires_ts FROM issues WHERE claimed_by IS NOT NULL'
    )
    op.drop_constraint(op.f('ck_issues_issue_claim_only_in_progress'), 'issues', type_='check')
    op.drop_constraint(op.f('ck_issues_issue_claim_is_whole'), 'issues', type_='check')
    op.drop_column('issues', 'claimed_by')
    op.drop_column('issues', 'claim_expires_ts')
    op.execute("UPDATE issues SET status = 'open' WHERE status = 'in_progress'")
    op.execute("DELETE FROM issue_statuses WHERE name = 'in_progress'")
    for table in AUDITED_TABLES:
        op.execute(
            f'CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON "{table}" FOR EACH ROW EXECUTE FUNCTION audit_trail_record()'
        )


def downgrade() -> None:
    """Put each open issue's unended claim back on its row, refusing while any other claim exists.

    An ended claim, and a claim on an initiative or a closed issue, has no column to go back to. The
    downgrade stops before dropping them, while the database still matches its revision.
    """
    stranded = op.get_bind().scalar(
        sa.text(
            'SELECT count(*) FROM claims LEFT JOIN issues ON issues.number = claims.record_number '
            "WHERE claims.ended_ts IS NOT NULL OR issues.number IS NULL OR issues.status <> 'open'"
        )
    )
    if stranded:
        counted = '1 claim has' if stranded == 1 else f'{stranded} claims have'
        raise RuntimeError(
            f'{counted} no issue column to go back to: ended claims, and claims on an initiative or a closed issue. '
            'Restore the backup taken before the upgrade, or delete those claims and downgrade again.'
        )
    for table in AUDITED_TABLES:
        op.execute(f'DROP TRIGGER audit_trail ON "{table}"')
    op.execute("INSERT INTO issue_statuses (name) VALUES ('in_progress')")
    op.add_column('issues', sa.Column('claim_expires_ts', postgresql.TIMESTAMP(timezone=True), autoincrement=False, nullable=True))
    op.add_column('issues', sa.Column('claimed_by', sa.TEXT(), autoincrement=False, nullable=True))
    op.execute(
        "UPDATE issues SET status = 'in_progress', claimed_by = claims.holder, claim_expires_ts = claims.expires_ts "
        "FROM claims WHERE claims.record_number = issues.number AND claims.ended_ts IS NULL AND issues.status = 'open'"
    )
    op.create_check_constraint(op.f('ck_issues_issue_claim_is_whole'), 'issues', '(claimed_by IS NULL) = (claim_expires_ts IS NULL)')
    op.create_check_constraint(op.f('ck_issues_issue_claim_only_in_progress'), 'issues', "claimed_by IS NULL OR status = 'in_progress'")
    op.drop_index('uq_claims_one_unended_per_record', table_name='claims', postgresql_where=sa.text('ended_ts IS NULL'))
    op.drop_index(op.f('ix_claims_holder'), table_name='claims')
    op.drop_index('idx_claims_record_number', table_name='claims')
    op.drop_table('claims')
    op.drop_table('claim_endings')
