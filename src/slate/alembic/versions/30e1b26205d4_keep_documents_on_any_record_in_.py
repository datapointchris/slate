"""keep documents on any record in versions, and comments on any record or line

Revision ID: 30e1b26205d4
Revises: 743f2b6420ac
Create Date: 2026-11-02 08:36:01.235260

A record of any kind keeps research, design and plan documents, each a series of versions. A
comment sits on a record, or on one line of a document version. Every issue comment moves into
`comments` on its issue's number, its author kept as who wrote it.
"""

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

# revision identifiers, used by Alembic.
revision = '30e1b26205d4'
down_revision = '743f2b6420ac'
branch_labels = None
depends_on = None

AUDITED_TABLES = ('document_kinds', 'documents', 'document_versions', 'comments')


def upgrade() -> None:
    op.create_table(
        'document_kinds',
        sa.Column('name', sa.Text(), nullable=False),
        sa.PrimaryKeyConstraint('name', name=op.f('pk_document_kinds')),
    )
    op.create_table(
        'documents',
        sa.Column('id', sa.BigInteger(), sa.Identity(always=False), nullable=False),
        sa.Column('record_number', sa.BigInteger(), nullable=False),
        sa.Column('name', sa.Text(), nullable=False),
        sa.Column('kind', sa.Text(), nullable=False),
        sa.Column('created_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.CheckConstraint("name ~ '^[a-z0-9]+(-[a-z0-9]+)*$'", name=op.f('ck_documents_document_name_shape')),
        sa.ForeignKeyConstraint(['kind'], ['document_kinds.name'], name=op.f('fk_documents_kind_document_kinds')),
        sa.ForeignKeyConstraint(['record_number'], ['records.number'], name=op.f('fk_documents_record_number_records'), ondelete='CASCADE'),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_documents')),
        sa.UniqueConstraint('record_number', 'id', name='uq_documents_record_number_id'),
        sa.UniqueConstraint('record_number', 'name', name='uq_documents_one_name_on_a_record'),
    )
    op.create_table(
        'document_versions',
        sa.Column('document_id', sa.BigInteger(), nullable=False),
        sa.Column('version', sa.Integer(), nullable=False),
        sa.Column('body', sa.Text(), nullable=False),
        sa.Column('written_by', sa.Text(), nullable=False),
        sa.Column('session_id', sa.Text(), nullable=True),
        sa.Column('worklog_id', sa.BigInteger(), nullable=True),
        sa.Column('written_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.CheckConstraint("btrim(body) <> ''", name=op.f('ck_document_versions_document_body_not_blank')),
        sa.CheckConstraint('version >= 1', name=op.f('ck_document_versions_version_counts_from_one')),
        sa.ForeignKeyConstraint(
            ['document_id'], ['documents.id'], name=op.f('fk_document_versions_document_id_documents'), ondelete='CASCADE'
        ),
        sa.ForeignKeyConstraint(['session_id'], ['agent_sessions.id'], name=op.f('fk_document_versions_session_id_agent_sessions')),
        sa.ForeignKeyConstraint(
            ['worklog_id'], ['worklogs.id'], name=op.f('fk_document_versions_worklog_id_worklogs'), ondelete='SET NULL'
        ),
        sa.PrimaryKeyConstraint('document_id', 'version', name=op.f('pk_document_versions')),
    )
    op.create_index('idx_document_versions_worklog_id', 'document_versions', ['worklog_id'], unique=False)
    op.create_table(
        'comments',
        sa.Column('id', sa.BigInteger(), sa.Identity(always=False), nullable=False),
        sa.Column('record_number', sa.BigInteger(), nullable=False),
        sa.Column('document_id', sa.BigInteger(), nullable=True),
        sa.Column('version', sa.Integer(), nullable=True),
        sa.Column('line', sa.Integer(), nullable=True),
        sa.Column('body', sa.Text(), nullable=False),
        sa.Column('written_by', sa.Text(), nullable=True),
        sa.Column('session_id', sa.Text(), nullable=True),
        sa.Column('worklog_id', sa.BigInteger(), nullable=True),
        sa.Column('written_ts', sa.DateTime(timezone=True), server_default=sa.text('now()'), nullable=False),
        sa.CheckConstraint("btrim(body) <> ''", name=op.f('ck_comments_comment_body_not_blank')),
        sa.CheckConstraint(
            '(document_id IS NULL) = (version IS NULL) AND (version IS NULL) = (line IS NULL)',
            name=op.f('ck_comments_comment_line_is_whole'),
        ),
        sa.CheckConstraint('line IS NULL OR line >= 1', name=op.f('ck_comments_comment_line_counts_from_one')),
        sa.ForeignKeyConstraint(
            ['document_id', 'version'],
            ['document_versions.document_id', 'document_versions.version'],
            name=op.f('fk_comments_document_id_document_versions'),
            ondelete='CASCADE',
        ),
        sa.ForeignKeyConstraint(
            ['record_number', 'document_id'],
            ['documents.record_number', 'documents.id'],
            name=op.f('fk_comments_record_number_documents'),
            ondelete='CASCADE',
        ),
        sa.ForeignKeyConstraint(['record_number'], ['records.number'], name=op.f('fk_comments_record_number_records'), ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['session_id'], ['agent_sessions.id'], name=op.f('fk_comments_session_id_agent_sessions')),
        sa.ForeignKeyConstraint(['worklog_id'], ['worklogs.id'], name=op.f('fk_comments_worklog_id_worklogs'), ondelete='SET NULL'),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_comments')),
    )
    op.create_index('idx_comments_document_id_version', 'comments', ['document_id', 'version'], unique=False)
    op.create_index('idx_comments_record_number', 'comments', ['record_number'], unique=False)
    op.execute("INSERT INTO document_kinds (name) VALUES ('research'), ('design'), ('plan')")
    op.execute(
        'INSERT INTO comments (record_number, body, written_by, written_ts) '
        'SELECT issues.number, issue_comments.body, issue_comments.author, issue_comments.created_ts '
        'FROM issue_comments JOIN issues ON issues.id = issue_comments.issue_id '
        'ORDER BY issue_comments.created_ts, issue_comments.id'
    )
    op.drop_index(op.f('ix_issue_comments_issue_id'), table_name='issue_comments')
    op.drop_table('issue_comments')
    for table in AUDITED_TABLES:
        op.execute(
            f'CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON "{table}" FOR EACH ROW EXECUTE FUNCTION audit_trail_record()'
        )


def downgrade() -> None:
    """Put each comment on an issue back in `issue_comments`, refusing while anything else exists.

    A document, a line comment and a comment on an idea or an initiative have nowhere to go back to.
    The downgrade stops before dropping them, while the database still matches its revision.
    """
    stranded = op.get_bind().scalar(
        sa.text(
            'SELECT (SELECT count(*) FROM documents) + (SELECT count(*) FROM comments LEFT JOIN issues '
            'ON issues.number = comments.record_number WHERE comments.document_id IS NOT NULL OR issues.number IS NULL)'
        )
    )
    if stranded:
        raise RuntimeError(
            f'{stranded} documents and comments have no table to go back to: documents, line comments, and comments '
            'on an idea or an initiative. Restore the backup taken before the upgrade, or delete them and downgrade again.'
        )
    for table in AUDITED_TABLES:
        op.execute(f'DROP TRIGGER audit_trail ON "{table}"')
    op.create_table(
        'issue_comments',
        sa.Column('id', sa.UUID(), autoincrement=False, nullable=False),
        sa.Column('issue_id', sa.UUID(), autoincrement=False, nullable=False),
        sa.Column('body', sa.TEXT(), autoincrement=False, nullable=False),
        sa.Column('author', sa.TEXT(), autoincrement=False, nullable=True),
        sa.Column('created_ts', postgresql.TIMESTAMP(timezone=True), server_default=sa.text('now()'), autoincrement=False, nullable=False),
        sa.ForeignKeyConstraint(['issue_id'], ['issues.id'], name=op.f('fk_issue_comments_issue_id_issues'), ondelete='CASCADE'),
        sa.PrimaryKeyConstraint('id', name=op.f('pk_issue_comments')),
    )
    op.create_index(op.f('ix_issue_comments_issue_id'), 'issue_comments', ['issue_id'], unique=False)
    op.execute(
        'INSERT INTO issue_comments (id, issue_id, body, author, created_ts) '
        'SELECT gen_random_uuid(), issues.id, comments.body, comments.written_by, comments.written_ts '
        'FROM comments JOIN issues ON issues.number = comments.record_number'
    )
    op.execute(
        'CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON "issue_comments" FOR EACH ROW EXECUTE FUNCTION audit_trail_record()'
    )
    op.drop_index('idx_comments_record_number', table_name='comments')
    op.drop_index('idx_comments_document_id_version', table_name='comments')
    op.drop_table('comments')
    op.drop_index('idx_document_versions_worklog_id', table_name='document_versions')
    op.drop_table('document_versions')
    op.drop_table('documents')
    op.drop_table('document_kinds')
