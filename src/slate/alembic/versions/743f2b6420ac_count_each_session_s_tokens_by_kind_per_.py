"""count each session's tokens by kind, per message, in the span it was spent

Revision ID: 743f2b6420ac
Revises: 8e4628bfb751
Create Date: 2026-11-02 08:01:00.894662

Each row of `message_tokens` is one model message a session reported: its tokens by kind, when it
was written, and the worklog span the session was in then. A report replaces a session's rows, and
`agent_sessions.usage_reported_ts` says when one last changed them. The rows mirror a transcript, so
the table carries no audit trigger.
"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = '743f2b6420ac'
down_revision = '8e4628bfb751'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        'message_tokens',
        sa.Column('session_id', sa.Text(), nullable=False),
        sa.Column('key', sa.Text(), nullable=False),
        sa.Column('written_ts', sa.DateTime(timezone=True), nullable=False),
        sa.Column('model', sa.Text(), nullable=True),
        sa.Column('subagent', sa.Text(), nullable=True),
        sa.Column('uncached_input_tokens', sa.Integer(), nullable=False),
        sa.Column('cache_write_tokens', sa.Integer(), nullable=False),
        sa.Column('cache_read_tokens', sa.Integer(), nullable=False),
        sa.Column('output_tokens', sa.Integer(), nullable=False),
        sa.Column('span_id', sa.BigInteger(), nullable=True),
        sa.CheckConstraint(
            'uncached_input_tokens >= 0 AND cache_write_tokens >= 0 AND cache_read_tokens >= 0 AND output_tokens >= 0',
            name=op.f('ck_message_tokens_token_counts_are_not_negative'),
        ),
        sa.ForeignKeyConstraint(
            ['session_id'], ['agent_sessions.id'], name=op.f('fk_message_tokens_session_id_agent_sessions'), ondelete='CASCADE'
        ),
        sa.ForeignKeyConstraint(
            ['span_id'], ['worklog_spans.id'], name=op.f('fk_message_tokens_span_id_worklog_spans'), ondelete='SET NULL'
        ),
        sa.PrimaryKeyConstraint('session_id', 'key', name=op.f('pk_message_tokens')),
    )
    op.create_index('idx_message_tokens_written_ts', 'message_tokens', ['written_ts'], unique=False)
    op.create_index('idx_message_tokens_span_id', 'message_tokens', ['span_id'], unique=False)
    op.add_column('agent_sessions', sa.Column('usage_reported_ts', sa.DateTime(timezone=True), nullable=True))


def downgrade() -> None:
    op.drop_column('agent_sessions', 'usage_reported_ts')
    op.drop_index('idx_message_tokens_span_id', table_name='message_tokens')
    op.drop_index('idx_message_tokens_written_ts', table_name='message_tokens')
    op.drop_table('message_tokens')
