"""hold a new filing at the status incoming rather than triage

Revision ID: 4efeb7e04e1c
Revises: 6d88eaec7184
Create Date: 2026-11-18 22:43:01.307935

`triage` named an act, and `incoming` says what is true of a filing nobody has placed yet. The
`triage` row stays, because the API one release behind still writes it. A trigger stores each such
write as `incoming`, so the new release reads every write either one takes while both serve.
"""

from alembic import op

# revision identifiers, used by Alembic.
revision = '4efeb7e04e1c'
down_revision = '6d88eaec7184'
branch_labels = None
depends_on = None

STORE_TRIAGE_AS_INCOMING = """
CREATE FUNCTION store_triage_as_incoming() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'triage' THEN
        NEW.status := 'incoming';
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER store_triage_as_incoming BEFORE INSERT OR UPDATE OF status ON issues
    FOR EACH ROW EXECUTE FUNCTION store_triage_as_incoming();
"""


def upgrade() -> None:
    op.execute("INSERT INTO issue_statuses (name) VALUES ('incoming')")
    op.execute(STORE_TRIAGE_AS_INCOMING)
    op.execute("UPDATE issues SET status = 'incoming' WHERE status = 'triage'")


def downgrade() -> None:
    op.execute('DROP TRIGGER store_triage_as_incoming ON issues')
    op.execute('DROP FUNCTION store_triage_as_incoming()')
    op.execute("UPDATE issues SET status = 'triage' WHERE status = 'incoming'")
    op.execute("DELETE FROM issue_statuses WHERE name = 'incoming'")
