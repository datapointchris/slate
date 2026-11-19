"""refuse the status triage, which every writer has replaced with incoming

Revision ID: 34d106f9667d
Revises: 74d59b6f8b72
Create Date: 2026-11-19 12:12:34.774441

The release before this one reads `triage` sent on a write as `incoming`, so it never stores it, and
the trigger stored any other writer's `triage` as `incoming`. With no writer left sending it, the
lookup row goes, and a write or filter naming `triage` is refused as an unknown status.
"""

from alembic import op

# revision identifiers, used by Alembic.
revision = '34d106f9667d'
down_revision = '74d59b6f8b72'
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
    op.execute('DROP TRIGGER store_triage_as_incoming ON issues')
    op.execute('DROP FUNCTION store_triage_as_incoming()')
    op.execute("UPDATE issues SET status = 'incoming' WHERE status = 'triage'")
    op.execute("DELETE FROM issue_statuses WHERE name = 'triage'")


def downgrade() -> None:
    op.execute("INSERT INTO issue_statuses (name) VALUES ('triage')")
    op.execute(STORE_TRIAGE_AS_INCOMING)
