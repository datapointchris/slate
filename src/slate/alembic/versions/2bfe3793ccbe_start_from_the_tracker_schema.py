"""start from the tracker's schema

Revision ID: 2bfe3793ccbe
Revises:
Create Date: 2026-11-02 03:17:42.369137

"""

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

# revision identifiers, used by Alembic.
revision = '2bfe3793ccbe'
down_revision = None
branch_labels = None
depends_on = None

# A new table takes its trigger in its own migration:
#   CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON <table>
#   FOR EACH ROW EXECUTE FUNCTION audit_trail_record('<secret column>', ...)
AUDITED_TABLES = (
    'initiative_statuses',
    'issue_statuses',
    'issue_types',
    'issue_labels',
    'initiatives',
    'issues',
    'issue_comments',
    'issue_dependencies',
    'issue_label_assignments',
)

RECORD_FUNCTION = """
CREATE FUNCTION audit_trail_record() RETURNS trigger LANGUAGE plpgsql AS $$
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

    INSERT INTO audit_trail (table_name, record_id, operation, changes, actor, request_id, job_run_id)
    VALUES (
        CASE WHEN TG_TABLE_SCHEMA = 'public' THEN TG_TABLE_NAME ELSE TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME END,
        coalesce(primary_key_text, ''),
        lower(TG_OP),
        written,
        nullif(current_setting('audit.actor', true), ''),
        nullif(current_setting('audit.request_id', true), ''),
        nullif(current_setting('audit.job_run_id', true), '')
    );
    RETURN NULL;
END;
$$
"""


def upgrade() -> None:
    # Issues and initiatives draw their numbers from it, so a bare number names one row.
    op.execute('CREATE SEQUENCE item_numbers AS bigint')
    op.create_table(
        'audit_trail',
        sa.Column('id', sa.BigInteger(), sa.Identity(always=True), nullable=False),
        sa.Column('changed_at', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.Column('table_name', sa.Text(), nullable=False),
        sa.Column('record_id', sa.Text(), nullable=False),
        sa.Column('operation', sa.Text(), nullable=False),
        sa.Column('changes', postgresql.JSONB(astext_type=sa.Text()), nullable=False),
        sa.Column('actor', sa.Text(), nullable=True),
        sa.Column('request_id', sa.Text(), nullable=True),
        sa.Column('job_run_id', sa.Text(), nullable=True),
        sa.CheckConstraint("operation IN ('insert', 'update', 'delete')", name=op.f('ck_audit_trail_operation_is_a_write')),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_audit_trail')),
        info={'record_changes': False},
    )
    op.create_index('idx_audit_trail_record', 'audit_trail', ['table_name', 'record_id', 'changed_at'], unique=False)
    op.create_index(op.f('ix_audit_trail_actor'), 'audit_trail', ['actor'], unique=False)
    op.create_index(op.f('ix_audit_trail_changed_at'), 'audit_trail', ['changed_at'], unique=False)
    op.create_table(
        'initiative_statuses',
        sa.Column('name', sa.Text(), nullable=False),
        sa.PrimaryKeyConstraint('name', name=op.f('pk_initiative_statuses')),
    )
    op.create_table(
        'issue_labels',
        sa.Column('id', sa.Integer(), sa.Identity(always=True), nullable=False),
        sa.Column('slug', sa.Text(), nullable=False),
        sa.Column('group_slug', sa.Text(), nullable=True),
        sa.Column('description', sa.Text(), nullable=True),
        sa.CheckConstraint(
            "group_slug IS NULL OR group_slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'", name=op.f('ck_issue_labels_issue_label_group_slug_shape')
        ),
        sa.CheckConstraint("slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'", name=op.f('ck_issue_labels_issue_label_slug_shape')),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_issue_labels')),
        sa.UniqueConstraint('slug', name=op.f('uq_issue_labels_slug')),
    )
    op.create_table(
        'issue_statuses', sa.Column('name', sa.Text(), nullable=False), sa.PrimaryKeyConstraint('name', name=op.f('pk_issue_statuses'))
    )
    op.create_table(
        'issue_types', sa.Column('name', sa.Text(), nullable=False), sa.PrimaryKeyConstraint('name', name=op.f('pk_issue_types'))
    )
    op.create_table(
        'initiatives',
        sa.Column('id', sa.Uuid(), nullable=False),
        sa.Column('number', sa.BigInteger(), server_default=sa.text("nextval('item_numbers')"), nullable=False),
        sa.Column('name', sa.Text(), nullable=False),
        sa.Column('description', sa.Text(), nullable=True),
        sa.Column('status', sa.Text(), server_default='active', nullable=False),
        sa.Column('status_reason', sa.Text(), nullable=True),
        sa.Column('priority', sa.SmallInteger(), server_default='0', nullable=False),
        sa.Column('position', sa.Integer(), server_default='0', nullable=False),
        sa.Column('autofix', sa.Boolean(), server_default=sa.text('true'), nullable=False),
        sa.Column('created_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.Column('closed_ts', sa.DateTime(timezone=True), nullable=True),
        sa.CheckConstraint(
            "status <> 'dropped' OR status_reason IS NOT NULL", name=op.f('ck_initiatives_initiative_dropped_requires_reason')
        ),
        sa.CheckConstraint('priority BETWEEN 0 AND 4', name=op.f('ck_initiatives_initiative_priority_range')),
        sa.ForeignKeyConstraint(['status'], ['initiative_statuses.name'], name=op.f('fk_initiatives_status_initiative_statuses')),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_initiatives')),
        sa.UniqueConstraint('number', name=op.f('uq_initiatives_number')),
    )
    op.create_index('uq_initiatives_name_active', 'initiatives', ['name'], unique=True, postgresql_where=sa.text("status = 'active'"))
    op.create_table(
        'issues',
        sa.Column('number', sa.BigInteger(), server_default=sa.text("nextval('item_numbers')"), nullable=False),
        sa.Column('title', sa.Text(), nullable=False),
        sa.Column('description', sa.Text(), nullable=True),
        sa.Column('acceptance', sa.Text(), nullable=True),
        sa.Column('repo', sa.Text(), nullable=True),
        sa.Column('type', sa.Text(), server_default='task', nullable=False),
        sa.Column('status', sa.Text(), server_default='open', nullable=False),
        sa.Column('status_reason', sa.Text(), nullable=True),
        sa.Column('priority', sa.SmallInteger(), server_default='0', nullable=False),
        sa.Column('deferred_until_date', sa.Date(), nullable=True),
        sa.Column('claimed_by', sa.Text(), nullable=True),
        sa.Column('claim_expires_ts', sa.DateTime(timezone=True), nullable=True),
        sa.Column('autofix', sa.Boolean(), server_default=sa.text('true'), nullable=False),
        sa.Column('initiative_id', sa.Uuid(), nullable=True),
        sa.Column('parent_id', sa.Uuid(), nullable=True),
        sa.Column('discovered_from_id', sa.Uuid(), nullable=True),
        sa.Column('duplicate_of_id', sa.Uuid(), nullable=True),
        sa.Column('updated_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.Column('closed_ts', sa.DateTime(timezone=True), nullable=True),
        sa.Column('closed_by', sa.Text(), nullable=True),
        sa.Column('id', sa.Uuid(), nullable=False),
        sa.Column('rank', sa.Double(), nullable=False),
        sa.Column('created_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.CheckConstraint("claimed_by IS NULL OR status = 'in_progress'", name=op.f('ck_issues_issue_claim_only_in_progress')),
        sa.CheckConstraint(
            "closed_by IS NULL OR status IN ('completed', 'canceled')", name=op.f('ck_issues_issue_closed_by_only_when_closed')
        ),
        sa.CheckConstraint(
            "closed_ts IS NULL OR status IN ('completed', 'canceled')", name=op.f('ck_issues_issue_closed_ts_only_when_closed')
        ),
        sa.CheckConstraint("duplicate_of_id IS NULL OR status = 'canceled'", name=op.f('ck_issues_issue_duplicate_only_when_canceled')),
        sa.CheckConstraint(
            "status <> 'canceled' OR status_reason IS NOT NULL OR duplicate_of_id IS NOT NULL",
            name=op.f('ck_issues_issue_canceled_requires_reason'),
        ),
        sa.CheckConstraint("status_reason IS NULL OR status = 'canceled'", name=op.f('ck_issues_issue_reason_only_when_canceled')),
        sa.CheckConstraint('(claimed_by IS NULL) = (claim_expires_ts IS NULL)', name=op.f('ck_issues_issue_claim_is_whole')),
        sa.CheckConstraint(
            'discovered_from_id IS NULL OR discovered_from_id <> id', name=op.f('ck_issues_issue_not_discovered_from_itself')
        ),
        sa.CheckConstraint('duplicate_of_id IS NULL OR duplicate_of_id <> id', name=op.f('ck_issues_issue_not_duplicate_of_itself')),
        sa.CheckConstraint('parent_id IS NULL OR parent_id <> id', name=op.f('ck_issues_issue_not_own_parent')),
        sa.CheckConstraint('priority BETWEEN 0 AND 4', name=op.f('ck_issues_issue_priority_range')),
        sa.ForeignKeyConstraint(
            ['discovered_from_id'], ['issues.id'], name=op.f('fk_issues_discovered_from_id_issues'), ondelete='SET NULL'
        ),
        sa.ForeignKeyConstraint(['duplicate_of_id'], ['issues.id'], name=op.f('fk_issues_duplicate_of_id_issues'), ondelete='SET NULL'),
        sa.ForeignKeyConstraint(
            ['initiative_id'], ['initiatives.id'], name=op.f('fk_issues_initiative_id_initiatives'), ondelete='SET NULL'
        ),
        sa.ForeignKeyConstraint(['parent_id'], ['issues.id'], name=op.f('fk_issues_parent_id_issues'), ondelete='SET NULL'),
        sa.ForeignKeyConstraint(['status'], ['issue_statuses.name'], name=op.f('fk_issues_status_issue_statuses')),
        sa.ForeignKeyConstraint(['type'], ['issue_types.name'], name=op.f('fk_issues_type_issue_types')),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_issues')),
        sa.UniqueConstraint('number', name=op.f('uq_issues_number')),
    )
    op.create_index('idx_issues_rank', 'issues', ['rank'], unique=False)
    op.create_index('idx_issues_status', 'issues', ['status'], unique=False)
    op.create_index(op.f('ix_issues_initiative_id'), 'issues', ['initiative_id'], unique=False)
    op.create_index(op.f('ix_issues_parent_id'), 'issues', ['parent_id'], unique=False)
    op.create_index(op.f('ix_issues_repo'), 'issues', ['repo'], unique=False)
    op.create_table(
        'issue_comments',
        sa.Column('id', sa.Uuid(), nullable=False),
        sa.Column('issue_id', sa.Uuid(), nullable=False),
        sa.Column('body', sa.Text(), nullable=False),
        sa.Column('author', sa.Text(), nullable=True),
        sa.Column('created_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.ForeignKeyConstraint(['issue_id'], ['issues.id'], name=op.f('fk_issue_comments_issue_id_issues'), ondelete='CASCADE'),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_issue_comments')),
    )
    op.create_index(op.f('ix_issue_comments_issue_id'), 'issue_comments', ['issue_id'], unique=False)
    op.create_table(
        'issue_dependencies',
        sa.Column('issue_id', sa.Uuid(), nullable=False),
        sa.Column('depends_on_id', sa.Uuid(), nullable=False),
        sa.CheckConstraint('issue_id <> depends_on_id', name=op.f('ck_issue_dependencies_issue_no_self_dependency')),
        sa.ForeignKeyConstraint(
            ['depends_on_id'], ['issues.id'], name=op.f('fk_issue_dependencies_depends_on_id_issues'), ondelete='CASCADE'
        ),
        sa.ForeignKeyConstraint(['issue_id'], ['issues.id'], name=op.f('fk_issue_dependencies_issue_id_issues'), ondelete='CASCADE'),
        sa.PrimaryKeyConstraint('issue_id', 'depends_on_id', name=op.f('pk_issue_dependencies')),
    )
    op.create_index('idx_issue_dependencies_depends_on', 'issue_dependencies', ['depends_on_id'], unique=False)
    op.create_table(
        'issue_label_assignments',
        sa.Column('issue_id', sa.Uuid(), nullable=False),
        sa.Column('label_id', sa.Integer(), nullable=False),
        sa.ForeignKeyConstraint(['issue_id'], ['issues.id'], name=op.f('fk_issue_label_assignments_issue_id_issues'), ondelete='CASCADE'),
        sa.ForeignKeyConstraint(
            ['label_id'], ['issue_labels.id'], name=op.f('fk_issue_label_assignments_label_id_issue_labels'), ondelete='CASCADE'
        ),
        sa.PrimaryKeyConstraint('issue_id', 'label_id', name=op.f('pk_issue_label_assignments')),
    )
    op.create_index('idx_issue_label_assignments_label', 'issue_label_assignments', ['label_id'], unique=False)

    op.execute("INSERT INTO issue_statuses (name) VALUES ('triage'), ('open'), ('in_progress'), ('completed'), ('canceled')")
    op.execute("INSERT INTO issue_types (name) VALUES ('bug'), ('feature'), ('task'), ('chore'), ('decision')")
    op.execute("INSERT INTO initiative_statuses (name) VALUES ('active'), ('someday'), ('completed'), ('dropped')")

    op.execute(RECORD_FUNCTION)
    for table in AUDITED_TABLES:
        op.execute(
            f'CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON "{table}" FOR EACH ROW EXECUTE FUNCTION audit_trail_record()'
        )


def downgrade() -> None:
    for table in AUDITED_TABLES:
        op.execute(f'DROP TRIGGER audit_trail ON "{table}"')
    op.execute('DROP FUNCTION audit_trail_record()')
    op.drop_index('idx_issue_label_assignments_label', table_name='issue_label_assignments')
    op.drop_table('issue_label_assignments')
    op.drop_index('idx_issue_dependencies_depends_on', table_name='issue_dependencies')
    op.drop_table('issue_dependencies')
    op.drop_index(op.f('ix_issue_comments_issue_id'), table_name='issue_comments')
    op.drop_table('issue_comments')
    op.drop_index(op.f('ix_issues_repo'), table_name='issues')
    op.drop_index(op.f('ix_issues_parent_id'), table_name='issues')
    op.drop_index(op.f('ix_issues_initiative_id'), table_name='issues')
    op.drop_index('idx_issues_status', table_name='issues')
    op.drop_index('idx_issues_rank', table_name='issues')
    op.drop_table('issues')
    op.drop_index('uq_initiatives_name_active', table_name='initiatives', postgresql_where=sa.text("status = 'active'"))
    op.drop_table('initiatives')
    op.drop_table('issue_types')
    op.drop_table('issue_statuses')
    op.drop_table('issue_labels')
    op.drop_table('initiative_statuses')
    op.drop_index(op.f('ix_audit_trail_changed_at'), table_name='audit_trail')
    op.drop_index(op.f('ix_audit_trail_actor'), table_name='audit_trail')
    op.drop_index('idx_audit_trail_record', table_name='audit_trail')
    op.drop_table('audit_trail')
    op.execute('DROP SEQUENCE item_numbers')
