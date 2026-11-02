"""drop the issue rank and initiative position no release reads

Revision ID: 3e89a5e22b1e
Revises: 205d1f5ac5c7
Create Date: 2026-11-02 09:51:21.749918

`records.rank` orders every record. `issues.rank`, its `issues_rank_from_record` trigger and
`initiatives.position` were kept for the release before it, and no release that reads them runs.

The downgrade puts them back filled from `records.rank`: each issue its record's rank, and each
initiative its place among the initiatives in that order.
"""

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = '3e89a5e22b1e'
down_revision = '205d1f5ac5c7'
branch_labels = None
depends_on = None

ISSUE_RANK_FROM_RECORD = """
CREATE FUNCTION issues_rank_from_record() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.rank IS NULL THEN
        NEW.rank := (SELECT rank FROM records WHERE number = NEW.number);
    END IF;
    RETURN NEW;
END
$$
"""


def upgrade() -> None:
    op.execute('DROP TRIGGER issues_rank_from_record ON issues')
    op.execute('DROP FUNCTION issues_rank_from_record()')
    op.drop_index('idx_issues_rank', table_name='issues')
    op.drop_column('issues', 'rank')
    op.drop_column('initiatives', 'position')


def downgrade() -> None:
    op.add_column('initiatives', sa.Column('position', sa.Integer(), server_default=sa.text('0'), nullable=False))
    op.execute("""
        UPDATE initiatives SET position = placed.position
        FROM (SELECT initiatives.number, row_number() OVER (ORDER BY records.rank) AS position
              FROM initiatives JOIN records USING (number)) placed
        WHERE initiatives.number = placed.number
    """)

    op.add_column('issues', sa.Column('rank', sa.Double(), nullable=True))
    op.execute('UPDATE issues SET rank = records.rank FROM records WHERE records.number = issues.number')
    op.alter_column('issues', 'rank', nullable=False)
    op.create_index('idx_issues_rank', 'issues', ['rank'], unique=False)
    op.execute(ISSUE_RANK_FROM_RECORD)
    op.execute('CREATE TRIGGER issues_rank_from_record BEFORE INSERT ON issues FOR EACH ROW EXECUTE FUNCTION issues_rank_from_record()')
