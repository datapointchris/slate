"""keep the trace and baggage of each write in the audit trail

Revision ID: 1a7f84e9a913
Revises: e2eebda09b7b
Create Date: 2026-11-02 12:26:32.976258

"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = '1a7f84e9a913'
down_revision = 'e2eebda09b7b'
branch_labels = None
depends_on = None

# The trigger function as the first revision wrote it, with its INSERT left as a slot. A writer that
# sets neither trace setting still writes through it, and an unset custom setting reads as '' once an
# earlier transaction on the same connection set it, so each is read through NULLIF.
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

INSERT_WITHOUT_TRACE = """INSERT INTO audit_trail (table_name, record_id, operation, changes, actor, request_id, job_run_id)
    VALUES (
        CASE WHEN TG_TABLE_SCHEMA = 'public' THEN TG_TABLE_NAME ELSE TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME END,
        coalesce(primary_key_text, ''),
        lower(TG_OP),
        written,
        nullif(current_setting('audit.actor', true), ''),
        nullif(current_setting('audit.request_id', true), ''),
        nullif(current_setting('audit.job_run_id', true), '')
    );"""


def upgrade() -> None:
    op.add_column('audit_trail', sa.Column('traceparent', sa.Text(), nullable=True))
    op.add_column('audit_trail', sa.Column('baggage', sa.Text(), nullable=True))
    op.execute(RECORD_FUNCTION.replace('@INSERT@', INSERT_WITH_TRACE))


def downgrade() -> None:
    op.execute(RECORD_FUNCTION.replace('@INSERT@', INSERT_WITHOUT_TRACE))
    op.drop_column('audit_trail', 'baggage')
    op.drop_column('audit_trail', 'traceparent')
