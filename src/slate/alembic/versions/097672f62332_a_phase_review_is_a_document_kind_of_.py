"""a phase review is a document kind of its own

Revision ID: 097672f62332
Revises: 7faa0da8bfb9
Create Date: 2026-11-17 10:40:12.000000

A phase review checks the built work against the plan mid-build. With no kind for it, each was
written as a plan named `phase-<n>-review`, so those become reviews. The release before reads a
review, since a read carries the kind as text, but refuses to create one.

A document named for a kind holds only that kind, and the CLI writes one by its name with the name
as its kind. So one already named `review`, which no kind could match before, takes the name the
API gives that case, `review-<kind>`. The downgrade leaves it so named.
"""

from alembic import op

# revision identifiers, used by Alembic.
revision = '097672f62332'
down_revision = '7faa0da8bfb9'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("INSERT INTO document_kinds (name) VALUES ('review')")
    op.execute("UPDATE documents SET kind = 'review' WHERE kind = 'plan' AND name ~ '^phase-[0-9]+-review$'")
    op.execute("UPDATE documents SET name = 'review-' || kind WHERE name = 'review'")


def downgrade() -> None:
    op.execute("UPDATE documents SET kind = 'plan' WHERE kind = 'review'")
    op.execute("DELETE FROM document_kinds WHERE name = 'review'")
