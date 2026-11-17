"""key what the audit trail records on each table's schema and name

Revision ID: 6d88eaec7184
Revises: 4d16e0679e68
Create Date: 2026-11-17 11:32:22.177701

`audit_trail_tables` was keyed on a regclass, which only a table that exists can hold, and
which a dropped table leaves as a bare oid. It is keyed on the table's schema and name
instead, as information_schema is, so a table is declared before it exists. env.py rewrites
every model's row at head, so the table is recreated with its two seed rows rather than
converted.
"""

from alembic import op

# revision identifiers, used by Alembic.
revision = '6d88eaec7184'
down_revision = '4d16e0679e68'
branch_labels = None
depends_on = None

DROP_DECLARATIONS = """
DROP FUNCTION audit_trail_reconcile();
DROP VIEW audit_trail_targets;
DROP TABLE audit_trail_tables;
"""

CREATE_NAMED_DECLARATIONS = """
CREATE TABLE audit_trail_tables (
    table_schema text NOT NULL,
    table_name text NOT NULL,
    is_recorded boolean NOT NULL DEFAULT true,
    ignored_columns text[] NOT NULL DEFAULT '{}',
    PRIMARY KEY (table_schema, table_name)
);

INSERT INTO audit_trail_tables (table_schema, table_name, is_recorded) VALUES
    ('public', 'audit_trail', false),
    ('public', 'message_tokens', false);

CREATE VIEW audit_trail_targets AS
SELECT
    c.oid::regclass AS audited_table,
    coalesce(d.is_recorded, true) AS is_recorded,
    coalesce(d.ignored_columns, '{}') AS ignored_columns,
    EXISTS (SELECT FROM pg_trigger AS t WHERE t.tgrelid = c.oid AND t.tgname = 'audit_trail') AS is_attached
FROM pg_class AS c
JOIN pg_namespace AS n ON n.oid = c.relnamespace
LEFT JOIN audit_trail_tables AS d ON d.table_schema = n.nspname AND d.table_name = c.relname
WHERE c.relkind IN ('r', 'p')
    AND NOT c.relispartition
    AND n.nspname NOT IN ('pg_catalog', 'information_schema')
    AND n.nspname NOT LIKE 'pg\\_%'
    AND c.relname NOT IN ('alembic_version', 'audit_trail_tables');
"""

CREATE_REGCLASS_DECLARATIONS = """
CREATE TABLE audit_trail_tables (
    audited_table regclass PRIMARY KEY,
    is_recorded boolean NOT NULL DEFAULT true,
    ignored_columns text[] NOT NULL DEFAULT '{}'
);

INSERT INTO audit_trail_tables (audited_table, is_recorded) VALUES
    ('audit_trail', false),
    ('message_tokens', false);

CREATE VIEW audit_trail_targets AS
SELECT
    c.oid::regclass AS audited_table,
    coalesce(d.is_recorded, true) AS is_recorded,
    coalesce(d.ignored_columns, '{}') AS ignored_columns,
    EXISTS (SELECT FROM pg_trigger AS t WHERE t.tgrelid = c.oid AND t.tgname = 'audit_trail') AS is_attached
FROM pg_class AS c
JOIN pg_namespace AS n ON n.oid = c.relnamespace
LEFT JOIN audit_trail_tables AS d ON d.audited_table = c.oid
WHERE c.relkind IN ('r', 'p')
    AND NOT c.relispartition
    AND n.nspname NOT IN ('pg_catalog', 'information_schema')
    AND n.nspname NOT LIKE 'pg\\_%'
    AND c.relname NOT IN ('alembic_version', 'audit_trail_tables');
"""

RECONCILE_FUNCTION = """
CREATE FUNCTION audit_trail_reconcile() RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    copied text;
    target audit_trail_targets;
BEGIN
    SELECT string_agg(format('%s.%I', t.audited_table, a.attname), ', ') INTO copied
    FROM audit_trail_targets AS t
    JOIN pg_attribute AS a ON a.attrelid = t.audited_table AND a.attnum > 0 AND NOT a.attisdropped
    WHERE t.is_recorded
        AND a.attname ~ '(^|_)(password|secret|token|api_key)$'
        AND a.attname <> ALL (t.ignored_columns);
    IF copied IS NOT NULL THEN
        RAISE EXCEPTION 'the audit trail would copy %; list each in its table''s ignored_columns', copied;
    END IF;

    FOR target IN SELECT * FROM audit_trail_targets WHERE is_recorded <> is_attached LOOP
        IF target.is_recorded THEN
            EXECUTE format('CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON %s '
                           'FOR EACH ROW EXECUTE FUNCTION audit_trail_record()', target.audited_table);
        ELSE
            EXECUTE format('DROP TRIGGER audit_trail ON %s', target.audited_table);
        END IF;
    END LOOP;
END;
$$;
"""

# The trigger function reading its table's ignored columns by schema and name.
RECORD_FUNCTION_BY_NAME = """
CREATE OR REPLACE FUNCTION audit_trail_record() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp AS $$
DECLARE
    ignored text[] := coalesce(
        (SELECT ignored_columns FROM audit_trail_tables WHERE table_schema = TG_TABLE_SCHEMA AND table_name = TG_TABLE_NAME),
        '{}'
    );
    old_row jsonb := CASE WHEN TG_OP <> 'INSERT' THEN to_jsonb(OLD) - ignored END;
    new_row jsonb := CASE WHEN TG_OP <> 'DELETE' THEN to_jsonb(NEW) - ignored END;
    primary_key_text text;
    written jsonb;
BEGIN
    SELECT string_agg(coalesce(new_row, old_row) ->> a.attname, ',' ORDER BY k.position) INTO primary_key_text
    FROM pg_index AS i
    CROSS JOIN LATERAL unnest(i.indkey::int2[]) WITH ORDINALITY AS k(attnum, position)
    JOIN pg_attribute AS a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
    WHERE i.indrelid = TG_RELID AND i.indisprimary;

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

    INSERT INTO audit_trail
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
    );
    RETURN NULL;
END;
$$;
"""

# The function as 4d16e0679e68 left it, reading them by regclass.
RECORD_FUNCTION_BY_REGCLASS = """
CREATE OR REPLACE FUNCTION audit_trail_record() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp AS $$
DECLARE
    ignored text[] := coalesce((SELECT ignored_columns FROM audit_trail_tables WHERE audited_table = TG_RELID), '{}');
    old_row jsonb := CASE WHEN TG_OP <> 'INSERT' THEN to_jsonb(OLD) - ignored END;
    new_row jsonb := CASE WHEN TG_OP <> 'DELETE' THEN to_jsonb(NEW) - ignored END;
    primary_key_text text;
    written jsonb;
BEGIN
    SELECT string_agg(coalesce(new_row, old_row) ->> a.attname, ',' ORDER BY k.position) INTO primary_key_text
    FROM pg_index AS i
    CROSS JOIN LATERAL unnest(i.indkey::int2[]) WITH ORDINALITY AS k(attnum, position)
    JOIN pg_attribute AS a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
    WHERE i.indrelid = TG_RELID AND i.indisprimary;

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

    INSERT INTO audit_trail
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
    );
    RETURN NULL;
END;
$$;
"""

# The app role is the database's name with `_app` after it. A database with no such role,
# such as a test's, has nothing to narrow.
NARROW_APP_ROLE = """
DO $$
DECLARE
    app_role text := current_database() || '_app';
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = app_role) THEN
        EXECUTE format('REVOKE INSERT, UPDATE, DELETE ON audit_trail_tables FROM %I', app_role);
    END IF;
END;
$$;
"""


def upgrade() -> None:
    op.execute(DROP_DECLARATIONS)
    op.execute(CREATE_NAMED_DECLARATIONS)
    op.execute(RECONCILE_FUNCTION)
    op.execute(RECORD_FUNCTION_BY_NAME)
    op.execute(NARROW_APP_ROLE)


def downgrade() -> None:
    op.execute(DROP_DECLARATIONS)
    op.execute(CREATE_REGCLASS_DECLARATIONS)
    op.execute(RECONCILE_FUNCTION)
    op.execute(RECORD_FUNCTION_BY_REGCLASS)
    op.execute(NARROW_APP_ROLE)
