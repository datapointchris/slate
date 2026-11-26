"""a claim moves between steps in one write, a wait stays on its worklog, and a document version keeps fields

Revision ID: 7a7a0e314de0
Revises: f721a977feb1
Create Date: 2026-11-26 01:02:17.477070

`claims advance` ends a worklog `moved_back` and `claims adopt` ends a claim `adopted`, so both
become rows the outcome and ending keys accept. Every new column is nullable, and the release
before reads none of them.

Each wait recorded before takes the worklog whose span held its claim's session on the claim's
record when the wait began. A wait with no such span, as on a claim no session held, takes the
claim's own worklog, which such a claim never left.
"""

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

# revision identifiers, used by Alembic.
revision = '7a7a0e314de0'
down_revision = 'f721a977feb1'
branch_labels = None
depends_on = None

WAIT_ON_ITS_SESSIONS_WORKLOG = """
UPDATE claim_waits AS wait
SET worklog_id = (
    SELECT span.worklog_id
    FROM worklog_spans AS span
    JOIN worklogs AS worklog ON worklog.id = span.worklog_id
    WHERE worklog.record_number = claim.record_number
      AND span.session_id = claim.session
      AND span.started_ts <= wait.started_ts
      AND (span.ended_ts IS NULL OR span.ended_ts > wait.started_ts)
    ORDER BY span.started_ts DESC, span.id DESC
    LIMIT 1
)
FROM claims AS claim
WHERE claim.id = wait.claim_id AND claim.session IS NOT NULL
"""

WAIT_ON_ITS_CLAIMS_WORKLOG = """
UPDATE claim_waits AS wait
SET worklog_id = worklog.id
FROM worklogs AS worklog
WHERE worklog.claim_id = wait.claim_id AND wait.worklog_id IS NULL
"""

ACTOR = 'alembic/7a7a0e314de0'


def sign_as(actor: str) -> None:
    # The setting lasts the run's transaction, so the caller clears it before the next migration in the run.
    op.execute(sa.select(sa.func.set_config('audit.actor', actor, True)))


def upgrade() -> None:
    sign_as(ACTOR)
    op.execute("INSERT INTO claim_endings (name) VALUES ('adopted')")
    op.execute("INSERT INTO worklog_outcomes (name) VALUES ('moved_back'), ('adopted')")
    op.add_column('claim_waits', sa.Column('worklog_id', sa.BigInteger(), nullable=True))
    op.create_index('idx_claim_waits_worklog_id', 'claim_waits', ['worklog_id'], unique=False)
    op.create_foreign_key(
        op.f('fk_claim_waits_worklog_id_worklogs'), 'claim_waits', 'worklogs', ['worklog_id'], ['id'], ondelete='SET NULL'
    )
    op.add_column('document_versions', sa.Column('fields', postgresql.JSONB(astext_type=sa.Text()), nullable=True))
    op.add_column('worklogs', sa.Column('follows_id', sa.BigInteger(), nullable=True))
    op.create_foreign_key(op.f('fk_worklogs_follows_id_worklogs'), 'worklogs', 'worklogs', ['follows_id'], ['id'], ondelete='SET NULL')
    op.execute(WAIT_ON_ITS_SESSIONS_WORKLOG)
    op.execute(WAIT_ON_ITS_CLAIMS_WORKLOG)
    sign_as('')


def downgrade() -> None:
    sign_as(ACTOR)
    op.drop_constraint(op.f('fk_worklogs_follows_id_worklogs'), 'worklogs', type_='foreignkey')
    op.drop_column('worklogs', 'follows_id')
    op.drop_column('document_versions', 'fields')
    op.drop_constraint(op.f('fk_claim_waits_worklog_id_worklogs'), 'claim_waits', type_='foreignkey')
    op.drop_index('idx_claim_waits_worklog_id', table_name='claim_waits')
    op.drop_column('claim_waits', 'worklog_id')
    # `expired` checks no step, as neither of these does.
    op.execute("UPDATE worklogs SET outcome = 'expired' WHERE outcome IN ('moved_back', 'adopted')")
    op.execute("UPDATE claims SET ending = 'released' WHERE ending = 'adopted'")
    op.execute("DELETE FROM worklog_outcomes WHERE name IN ('moved_back', 'adopted')")
    op.execute("DELETE FROM claim_endings WHERE name = 'adopted'")
    sign_as('')
