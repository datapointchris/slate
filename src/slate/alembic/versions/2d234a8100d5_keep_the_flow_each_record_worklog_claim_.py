"""keep the flow each record, worklog, claim, comment, version and write was made under

Revision ID: 2d234a8100d5
Revises: 53ce87dbb413
Create Date: 2026-11-16 20:29:49.950045

"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = '2d234a8100d5'
down_revision = '53ce87dbb413'
branch_labels = None
depends_on = None

# A row on each of these keeps the flow of the write that made it, from the setting the API sets
# per transaction. A worklog takes its record's flow, so it has no default.
WRITERS_FLOW = "nullif(current_setting('audit.flow_id', true), '')"
KEEPS_ITS_WRITERS_FLOW = ['records', 'claims', 'comments', 'document_versions']

# The trigger function as 1a7f84e9a913 wrote it, with its INSERT left as a slot.
RECORD_FUNCTION = """
CREATE OR REPLACE FUNCTION audit_trail_record() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    old_row jsonb := CASE WHEN TG_OP <> 'INSERT' THEN to_jsonb(OLD) END;
    new_row jsonb := CASE WHEN TG_OP <> 'DELETE' THEN to_jsonb(NEW) END;
    primary_key_text text;
    written jsonb;
BEGIN
    SELECT string_agg(coalesce(new_row, old_row) ->> a.attname, ',' ORDER BY k.position) INTO primary_key_text
    FROM pg_index AS i
    CROSS JOIN LATERAL unnest(i.indkey::int2[]) WITH ORDINALITY AS k(attnum, position)
    JOIN pg_attribute AS a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
    WHERE i.indrelid = TG_RELID AND i.indisprimary;

    old_row := old_row - coalesce(TG_ARGV, '{}'::text[]);
    new_row := new_row - coalesce(TG_ARGV, '{}'::text[]);
    IF TG_OP = 'INSERT' THEN
        SELECT jsonb_object_agg(n.key, jsonb_build_object('new', n.value)) INTO written FROM jsonb_each(new_row) AS n;
    ELSIF TG_OP = 'DELETE' THEN
        SELECT jsonb_object_agg(o.key, jsonb_build_object('old', o.value)) INTO written FROM jsonb_each(old_row) AS o;
    ELSE
        SELECT jsonb_object_agg(n.key, jsonb_build_object('old', o.value, 'new', n.value)) INTO written
        FROM jsonb_each(new_row) AS n JOIN jsonb_each(old_row) AS o ON o.key = n.key
        WHERE n.value IS DISTINCT FROM o.value;
    END IF;
    IF written IS NULL THEN
        RETURN NULL;
    END IF;

    @INSERT@
    RETURN NULL;
END;
$$
"""

INSERT_WITH_FLOW = """INSERT INTO audit_trail
        (table_name, record_id, operation, changes, actor, request_id, job_run_id, traceparent, baggage, flow_id)
    VALUES (
        CASE WHEN TG_TABLE_SCHEMA = 'public' THEN TG_TABLE_NAME ELSE TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME END,
        coalesce(primary_key_text, ''),
        lower(TG_OP),
        written,
        nullif(current_setting('audit.actor', true), ''),
        nullif(current_setting('audit.request_id', true), ''),
        nullif(current_setting('audit.job_run_id', true), ''),
        nullif(current_setting('audit.traceparent', true), ''),
        nullif(current_setting('audit.baggage', true), ''),
        nullif(current_setting('audit.flow_id', true), '')
    );"""

INSERT_WITH_TRACE = """INSERT INTO audit_trail
        (table_name, record_id, operation, changes, actor, request_id, job_run_id, traceparent, baggage)
    VALUES (
        CASE WHEN TG_TABLE_SCHEMA = 'public' THEN TG_TABLE_NAME ELSE TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME END,
        coalesce(primary_key_text, ''),
        lower(TG_OP),
        written,
        nullif(current_setting('audit.actor', true), ''),
        nullif(current_setting('audit.request_id', true), ''),
        nullif(current_setting('audit.job_run_id', true), ''),
        nullif(current_setting('audit.traceparent', true), ''),
        nullif(current_setting('audit.baggage', true), '')
    );"""


def upgrade() -> None:
    op.add_column('audit_trail', sa.Column('flow_id', sa.Text(), nullable=True))
    op.add_column('worklogs', sa.Column('flow_id', sa.Text(), nullable=True))
    # Each column is added bare and given its default after, so every existing row stays null.
    for table in KEEPS_ITS_WRITERS_FLOW:
        op.add_column(table, sa.Column('flow_id', sa.Text(), nullable=True))
        op.alter_column(table, 'flow_id', server_default=sa.text(WRITERS_FLOW))
    op.execute(RECORD_FUNCTION.replace('@INSERT@', INSERT_WITH_FLOW))


def downgrade() -> None:
    op.execute(RECORD_FUNCTION.replace('@INSERT@', INSERT_WITH_TRACE))
    for table in reversed(KEEPS_ITS_WRITERS_FLOW):
        op.drop_column(table, 'flow_id')
    op.drop_column('worklogs', 'flow_id')
    op.drop_column('audit_trail', 'flow_id')
