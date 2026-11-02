"""record each go at a record in a worklog, and each wait on a claim

Revision ID: 8e4628bfb751
Revises: eb361f914d1a
Create Date: 2026-11-02 07:04:11.112934

A worklog is one go at one record, with the agent sessions that joined it for
spans of time and the commits, pull requests and landings it produced. A claim
records each stretch its holder spent waiting. Every session a claim already
names becomes an `agent_sessions` row before the claim references it.
"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = '8e4628bfb751'
down_revision = 'eb361f914d1a'
branch_labels = None
depends_on = None

AUDITED_TABLES = (
    'agent_sessions',
    'worklog_link_kinds',
    'worklog_outcomes',
    'worklog_workflows',
    'claim_waits',
    'worklogs',
    'worklog_links',
    'worklog_spans',
)


def upgrade() -> None:
    op.create_table(
        'agent_sessions',
        sa.Column('id', sa.Text(), nullable=False),
        sa.Column('host', sa.Text(), nullable=True),
        sa.Column('first_seen_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_agent_sessions')),
    )
    op.create_table(
        'worklog_link_kinds',
        sa.Column('name', sa.Text(), nullable=False),
        sa.PrimaryKeyConstraint('name', name=op.f('pk_worklog_link_kinds')),
    )
    op.create_table(
        'worklog_outcomes',
        sa.Column('name', sa.Text(), nullable=False),
        sa.PrimaryKeyConstraint('name', name=op.f('pk_worklog_outcomes')),
    )
    op.create_table(
        'worklog_workflows',
        sa.Column('name', sa.Text(), nullable=False),
        sa.PrimaryKeyConstraint('name', name=op.f('pk_worklog_workflows')),
    )
    op.create_table(
        'claim_waits',
        sa.Column('id', sa.BigInteger(), sa.Identity(always=False), nullable=False),
        sa.Column('claim_id', sa.BigInteger(), nullable=False),
        sa.Column('waiting_on', sa.Text(), nullable=False),
        sa.Column('started_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.Column('ended_ts', sa.DateTime(timezone=True), nullable=True),
        sa.Column('ended_by', sa.Text(), nullable=True),
        sa.CheckConstraint('ended_by IS NULL OR ended_ts IS NOT NULL', name=op.f('ck_claim_waits_wait_ended_by_only_when_ended')),
        sa.ForeignKeyConstraint(['claim_id'], ['claims.id'], name=op.f('fk_claim_waits_claim_id_claims'), ondelete='CASCADE'),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_claim_waits')),
    )
    op.create_index('idx_claim_waits_claim_id', 'claim_waits', ['claim_id'], unique=False)
    op.create_index(
        'uq_claim_waits_one_open_per_claim', 'claim_waits', ['claim_id'], unique=True, postgresql_where=sa.text('ended_ts IS NULL')
    )
    op.create_table(
        'worklogs',
        sa.Column('id', sa.BigInteger(), sa.Identity(always=False), nullable=False),
        sa.Column('record_number', sa.BigInteger(), nullable=False),
        sa.Column('sequence', sa.Integer(), nullable=False),
        sa.Column('claim_id', sa.BigInteger(), nullable=True),
        sa.Column('opened_by', sa.Text(), nullable=False),
        sa.Column('workflow', sa.Text(), nullable=True),
        sa.Column('started_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.Column('ended_ts', sa.DateTime(timezone=True), nullable=True),
        sa.Column('outcome', sa.Text(), nullable=True),
        sa.Column('ended_by', sa.Text(), nullable=True),
        sa.CheckConstraint('(ended_ts IS NULL) = (outcome IS NULL)', name=op.f('ck_worklogs_worklog_outcome_is_whole')),
        sa.CheckConstraint('ended_by IS NULL OR ended_ts IS NOT NULL', name=op.f('ck_worklogs_worklog_ended_by_only_when_ended')),
        sa.CheckConstraint('sequence >= 1', name=op.f('ck_worklogs_sequence_counts_from_one')),
        sa.ForeignKeyConstraint(['claim_id'], ['claims.id'], name=op.f('fk_worklogs_claim_id_claims'), ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['outcome'], ['worklog_outcomes.name'], name=op.f('fk_worklogs_outcome_worklog_outcomes')),
        sa.ForeignKeyConstraint(['record_number'], ['records.number'], name=op.f('fk_worklogs_record_number_records'), ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['workflow'], ['worklog_workflows.name'], name=op.f('fk_worklogs_workflow_worklog_workflows')),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_worklogs')),
        sa.UniqueConstraint('claim_id', name=op.f('uq_worklogs_claim_id')),
        sa.UniqueConstraint('record_number', 'sequence', name='uq_worklogs_one_per_sequence_on_a_record'),
    )
    op.create_table(
        'worklog_links',
        sa.Column('id', sa.BigInteger(), sa.Identity(always=False), nullable=False),
        sa.Column('worklog_id', sa.BigInteger(), nullable=False),
        sa.Column('kind', sa.Text(), nullable=False),
        sa.Column('ref', sa.Text(), nullable=False),
        sa.Column('repo', sa.Text(), nullable=True),
        sa.Column('session_id', sa.Text(), nullable=True),
        sa.Column('linked_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.Column('linked_by', sa.Text(), nullable=False),
        sa.ForeignKeyConstraint(['kind'], ['worklog_link_kinds.name'], name=op.f('fk_worklog_links_kind_worklog_link_kinds')),
        sa.ForeignKeyConstraint(['session_id'], ['agent_sessions.id'], name=op.f('fk_worklog_links_session_id_agent_sessions')),
        sa.ForeignKeyConstraint(['worklog_id'], ['worklogs.id'], name=op.f('fk_worklog_links_worklog_id_worklogs'), ondelete='CASCADE'),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_worklog_links')),
        sa.UniqueConstraint('worklog_id', 'kind', 'ref', name='uq_worklog_links_one_per_ref'),
    )
    op.create_table(
        'worklog_spans',
        sa.Column('id', sa.BigInteger(), sa.Identity(always=False), nullable=False),
        sa.Column('worklog_id', sa.BigInteger(), nullable=False),
        sa.Column('session_id', sa.Text(), nullable=False),
        sa.Column('directory', sa.Text(), nullable=True),
        sa.Column('started_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.Column('ended_ts', sa.DateTime(timezone=True), nullable=True),
        sa.CheckConstraint('ended_ts IS NULL OR ended_ts >= started_ts', name=op.f('ck_worklog_spans_span_ends_after_it_starts')),
        sa.ForeignKeyConstraint(['session_id'], ['agent_sessions.id'], name=op.f('fk_worklog_spans_session_id_agent_sessions')),
        sa.ForeignKeyConstraint(['worklog_id'], ['worklogs.id'], name=op.f('fk_worklog_spans_worklog_id_worklogs'), ondelete='CASCADE'),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_worklog_spans')),
    )
    op.create_index('idx_worklog_spans_session_id', 'worklog_spans', ['session_id'], unique=False)
    op.create_index('idx_worklog_spans_worklog_id', 'worklog_spans', ['worklog_id'], unique=False)
    op.create_index(
        'uq_worklog_spans_one_open_per_session', 'worklog_spans', ['session_id'], unique=True, postgresql_where=sa.text('ended_ts IS NULL')
    )
    op.execute("INSERT INTO worklog_link_kinds (name) VALUES ('commit'), ('pull_request'), ('landing')")
    op.execute(
        'INSERT INTO worklog_outcomes (name) VALUES '
        "('finished'), ('released'), ('expired'), ('promoted'), ('completed'), ('canceled'), ('dropped')"
    )
    op.execute("INSERT INTO worklog_workflows (name) VALUES ('oneshot'), ('plan-first'), ('design-first')")
    op.execute(
        'INSERT INTO agent_sessions (id, host, first_seen_ts) '
        'SELECT DISTINCT ON (session) session, host, started_ts FROM claims WHERE session IS NOT NULL ORDER BY session, started_ts'
    )
    op.create_foreign_key(op.f('fk_claims_session_agent_sessions'), 'claims', 'agent_sessions', ['session'], ['id'])
    for table in AUDITED_TABLES:
        op.execute(
            f'CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON "{table}" FOR EACH ROW EXECUTE FUNCTION audit_trail_record()'
        )


def downgrade() -> None:
    for table in AUDITED_TABLES:
        op.execute(f'DROP TRIGGER audit_trail ON "{table}"')
    op.drop_constraint(op.f('fk_claims_session_agent_sessions'), 'claims', type_='foreignkey')
    op.drop_index('uq_worklog_spans_one_open_per_session', table_name='worklog_spans', postgresql_where=sa.text('ended_ts IS NULL'))
    op.drop_index('idx_worklog_spans_worklog_id', table_name='worklog_spans')
    op.drop_index('idx_worklog_spans_session_id', table_name='worklog_spans')
    op.drop_table('worklog_spans')
    op.drop_table('worklog_links')
    op.drop_table('worklogs')
    op.drop_index('uq_claim_waits_one_open_per_claim', table_name='claim_waits', postgresql_where=sa.text('ended_ts IS NULL'))
    op.drop_index('idx_claim_waits_claim_id', table_name='claim_waits')
    op.drop_table('claim_waits')
    op.drop_table('worklog_workflows')
    op.drop_table('worklog_outcomes')
    op.drop_table('worklog_link_kinds')
    op.drop_table('agent_sessions')
