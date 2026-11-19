"""hold a new filing at the status incoming rather than triage

Revision ID: 4efeb7e04e1c
Revises: 6d88eaec7184
Create Date: 2026-11-18 22:43:01.307935

`triage` named an act, and `incoming` says what is true of a filing nobody has placed yet. The
`triage` row stays, because the API one release behind still writes it.
"""

from alembic import op

# revision identifiers, used by Alembic.
revision = '4efeb7e04e1c'
down_revision = '6d88eaec7184'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("INSERT INTO issue_statuses (name) VALUES ('incoming')")
    op.execute("UPDATE issues SET status = 'incoming' WHERE status = 'triage'")


def downgrade() -> None:
    op.execute("UPDATE issues SET status = 'triage' WHERE status = 'incoming'")
    op.execute("DELETE FROM issue_statuses WHERE name = 'incoming'")
