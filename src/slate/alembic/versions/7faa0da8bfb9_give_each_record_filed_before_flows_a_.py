"""give each record filed before flows a root flow, and its worklogs that flow

Revision ID: 7faa0da8bfb9
Revises: 2d234a8100d5
Create Date: 2026-11-17 10:34:07.330448

A record filed before 2d234a8100d5 has no flow, and a worklog takes its record's, so every worklog
opened on one held none. Each such idea, initiative and issue gets a root flow, once, and each of
its worklogs holding no flow takes it, so all the work under one record shares one flow. An
initiative promoted from an idea shares the idea's, as a promotion does now: the idea's flow where
it holds one, else one minted for the pair. A pursuit refuses a claim, so it holds no work and keeps
no flow.

The downgrade leaves the flows, because the release before reads any flow a record holds.
"""

from alembic import op

# revision identifiers, used by Alembic.
revision = '7faa0da8bfb9'
down_revision = '2d234a8100d5'
branch_labels = None
depends_on = None

# A record's root is the idea it was promoted from, else itself. A version 4 UUID's 32 hex digits
# are a flow id: lowercase, and never all zeros, since its version digit is 4. `roots` is
# materialized, so each root draws its flow once.
MINT_ROOT_FLOWS = """
WITH flowless AS (
    SELECT records.number, coalesce(ideas.number, records.number) AS root, idea_records.flow_id AS root_flow
    FROM records
    LEFT JOIN initiatives ON initiatives.number = records.number
    LEFT JOIN ideas ON ideas.id = initiatives.idea_id
    LEFT JOIN records AS idea_records ON idea_records.number = ideas.number
    WHERE records.flow_id IS NULL AND records.kind IN ('idea', 'initiative', 'issue')
),
roots AS MATERIALIZED (
    SELECT root, coalesce(max(root_flow), replace(gen_random_uuid()::text, '-', '')) AS flow_id
    FROM flowless
    GROUP BY root
),
minted AS (
    UPDATE records SET flow_id = roots.flow_id
    FROM flowless JOIN roots ON roots.root = flowless.root
    WHERE records.number = flowless.number
    RETURNING records.number, records.flow_id
)
UPDATE worklogs SET flow_id = minted.flow_id
FROM minted
WHERE worklogs.record_number = minted.number AND worklogs.flow_id IS NULL
"""


def upgrade() -> None:
    op.execute(MINT_ROOT_FLOWS)


def downgrade() -> None:
    pass
