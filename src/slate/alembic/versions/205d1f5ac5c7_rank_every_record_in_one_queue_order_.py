"""rank every record in one queue order, and name the paths an issue changes

Revision ID: 205d1f5ac5c7
Revises: 30e1b26205d4
Create Date: 2026-11-02 08:46:07.784078

`rank` on `records` orders ideas, initiatives and issues in one space. Every initiative is seeded
ahead of every issue in `position` order, every issue keeps its order, and every idea follows in
number order. Ranks are renumbered 1 to N in that order. A trigger places a record inserted with no
rank one past the last.

`issues.rank` and `initiatives.position` stay, because the API release before this one reads them
while the two overlap in a rollout. A trigger copies a new issue's record rank into `issues.rank`,
so that release reads every issue this one files.

An issue names the paths it changes in `issues.paths`.
"""

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

# revision identifiers, used by Alembic.
revision = '205d1f5ac5c7'
down_revision = '30e1b26205d4'
branch_labels = None
depends_on = None

SEED_RANKS = """
UPDATE records SET rank = seeded.position
FROM (
    SELECT number, row_number() OVER (ORDER BY seeding_group, seeding_key, created_ts, number) AS position
    FROM (
        SELECT number, 0 AS seeding_group, position::double precision AS seeding_key, created_ts FROM initiatives
        UNION ALL SELECT number, 1, rank, created_ts FROM issues
        UNION ALL SELECT number, 2, number::double precision, created_ts FROM ideas
    ) placed
) seeded
WHERE records.number = seeded.number
"""

RANK_AT_END = """
CREATE FUNCTION records_rank_at_end() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.rank IS NULL THEN
        NEW.rank := coalesce((SELECT max(rank) FROM records), 0) + 1;
    END IF;
    RETURN NEW;
END
$$
"""

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
    op.add_column('records', sa.Column('rank', sa.Double(), nullable=True))
    op.execute(SEED_RANKS)
    op.alter_column('records', 'rank', nullable=False)
    op.create_index('idx_records_rank', 'records', ['rank'], unique=False)
    op.execute(RANK_AT_END)
    op.execute('CREATE TRIGGER records_rank_at_end BEFORE INSERT ON records FOR EACH ROW EXECUTE FUNCTION records_rank_at_end()')
    op.execute(ISSUE_RANK_FROM_RECORD)
    op.execute('CREATE TRIGGER issues_rank_from_record BEFORE INSERT ON issues FOR EACH ROW EXECUTE FUNCTION issues_rank_from_record()')

    op.add_column('issues', sa.Column('paths', postgresql.ARRAY(sa.Text()), server_default=sa.text("'{}'"), nullable=False))


def downgrade() -> None:
    op.execute("""
        UPDATE initiatives SET position = placed.position
        FROM (SELECT initiatives.number, row_number() OVER (ORDER BY records.rank) AS position
              FROM initiatives JOIN records USING (number)) placed
        WHERE initiatives.number = placed.number
    """)
    op.execute('UPDATE issues SET rank = records.rank FROM records WHERE records.number = issues.number')
    op.drop_column('issues', 'paths')

    op.execute('DROP TRIGGER issues_rank_from_record ON issues')
    op.execute('DROP FUNCTION issues_rank_from_record()')
    op.execute('DROP TRIGGER records_rank_at_end ON records')
    op.execute('DROP FUNCTION records_rank_at_end()')
    op.drop_index('idx_records_rank', table_name='records')
    op.drop_column('records', 'rank')
