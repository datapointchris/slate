"""give each record filed before flows a root flow, and its worklogs that flow

Revision ID: 7faa0da8bfb9
Revises: 2d234a8100d5
Create Date: 2026-11-17 10:34:07.330448

A record filed before 2d234a8100d5 has no flow, and a worklog takes its record's, so every worklog
opened on one held none. Each such idea, initiative and issue gets a root flow of its own, once,
and each of its worklogs holding no flow takes it, so all the work under one record shares one
flow. A pursuit refuses a claim, so it holds no work and keeps no flow.

The downgrade leaves the flows, because the release before reads any flow a record holds.
"""

from alembic import op

# revision identifiers, used by Alembic.
revision = '7faa0da8bfb9'
down_revision = '2d234a8100d5'
branch_labels = None
depends_on = None

# A version 4 UUID's 32 hex digits are a flow id: lowercase, and never all zeros, since its version digit is 4.
MINT_ROOT_FLOWS = """
WITH minted AS (
    UPDATE records SET flow_id = replace(gen_random_uuid()::text, '-', '')
    WHERE flow_id IS NULL AND kind IN ('idea', 'initiative', 'issue')
    RETURNING number, flow_id
)
UPDATE worklogs SET flow_id = minted.flow_id
FROM minted
WHERE worklogs.record_number = minted.number AND worklogs.flow_id IS NULL
"""


def upgrade() -> None:
    op.execute(MINT_ROOT_FLOWS)


def downgrade() -> None:
    pass
