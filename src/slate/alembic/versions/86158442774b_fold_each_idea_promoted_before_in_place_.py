"""fold each idea promoted before in-place promotion into its initiative

A pair is an idea and the initiative that names it in `initiatives.idea_id`. Each idea folds into
its initiative by the fold's steps, under reason `promoted` and dated at the initiative's
`created_ts`, and the initiative's record is marked promoted at that moment by the actor its
audit-trail insert names. The steps are written out as SQL here, so a later change to the fold
leaves what this migration did on a fresh database as it was.

Revision ID: 86158442774b
Revises: 765f75367cd0
Create Date: 2026-11-24 21:24:38.962110

"""

import logging

import sqlalchemy as sa
from alembic import op

# revision identifiers, used by Alembic.
revision = '86158442774b'
down_revision = '765f75367cd0'
branch_labels = None
depends_on = None

# The audit trail's actor for every write here, and the redirect's signer where no insert named one.
ACTOR = 'alembic/86158442774b'

logger = logging.getLogger('alembic.runtime.migration')

PAIRS = """
CREATE TEMP TABLE promoted_pairs AS
SELECT idea.number AS idea, initiative.number AS initiative, initiative.created_ts AS promoted_ts,
    (SELECT trail.actor FROM audit_trail trail
     WHERE trail.table_name = 'initiatives' AND trail.operation = 'insert' AND trail.record_id = initiative.id::text
     ORDER BY trail.id LIMIT 1) AS promoted_by,
    idea.id AS former_id, idea.title AS former_title, idea.priority AS former_priority,
    idea.created_ts AS former_created_ts, record.flow_id AS former_flow_id
FROM ideas idea
JOIN initiatives initiative ON initiative.idea_id = idea.id
JOIN records record ON record.number = idea.number
"""

WHAT_MOVES = """
SELECT pair.idea, pair.initiative, pair.promoted_ts, pair.promoted_by,
    (SELECT count(*) FROM worklogs moved WHERE moved.record_number = pair.idea) AS worklogs,
    (SELECT count(*) FROM claims moved WHERE moved.record_number = pair.idea) AS claims,
    (SELECT count(*) FROM comments moved WHERE moved.record_number = pair.idea AND moved.document_id IS NULL) AS comments,
    (SELECT count(*) FROM record_checks moved WHERE moved.record_number = pair.idea) AS checks,
    (SELECT count(*) FROM documents moved WHERE moved.record_number = pair.idea) AS documents,
    (SELECT count(*) FROM record_refs link WHERE link.record_number = pair.idea
     AND NOT EXISTS (SELECT 1 FROM record_refs held WHERE held.record_number = pair.initiative AND held.ref = link.ref)) AS refs,
    (SELECT count(*) FROM record_refs link WHERE link.record_number = pair.idea) AS refs_held,
    (SELECT count(*) FROM record_tags link WHERE link.record_number = pair.idea
     AND NOT EXISTS (SELECT 1 FROM record_tags held WHERE held.record_number = pair.initiative AND held.tag_id = link.tag_id)) AS tags,
    (SELECT count(*) FROM record_tags link WHERE link.record_number = pair.idea) AS tags_held,
    (SELECT count(*) FROM record_pursuits link WHERE link.record_number = pair.idea
     AND NOT EXISTS (SELECT 1 FROM record_pursuits held
                     WHERE held.record_number = pair.initiative AND held.pursuit_id = link.pursuit_id)) AS pursuits,
    (SELECT count(*) FROM record_pursuits link WHERE link.record_number = pair.idea) AS pursuits_held
FROM promoted_pairs pair
ORDER BY pair.idea
"""

FOLD = (
    # The initiative's record is marked promoted when the initiative was created.
    """
    UPDATE records record SET promoted_ts = pair.promoted_ts, promoted_by = pair.promoted_by
    FROM promoted_pairs pair WHERE record.number = pair.initiative
    """,
    # A record holds one unended claim, so an idea's unended claim ends where its initiative holds one,
    # with its open wait and its worklog. Elsewhere it moves with the other claims.
    """
    WITH ended AS (
        UPDATE claims claim
        SET ended_ts = least(now(), claim.expires_ts),
            ending = CASE WHEN claim.expires_ts <= now() THEN 'expired' ELSE 'released' END,
            ended_by = CASE WHEN claim.expires_ts <= now() THEN NULL ELSE :actor END
        FROM promoted_pairs pair
        WHERE claim.record_number = pair.idea AND claim.ended_ts IS NULL
          AND EXISTS (SELECT 1 FROM claims held WHERE held.record_number = pair.initiative AND held.ended_ts IS NULL)
        RETURNING claim.id, claim.ended_ts, claim.ending, claim.ended_by
    ), waits AS (
        UPDATE claim_waits wait SET ended_ts = ended.ended_ts, ended_by = ended.ended_by
        FROM ended WHERE wait.claim_id = ended.id AND wait.ended_ts IS NULL
    ), worklogs_ended AS (
        UPDATE worklogs worklog SET ended_ts = ended.ended_ts, outcome = ended.ending, ended_by = ended.ended_by
        FROM ended WHERE worklog.claim_id = ended.id AND worklog.ended_ts IS NULL
        RETURNING worklog.id, worklog.ended_ts
    )
    UPDATE worklog_spans span SET ended_ts = worklogs_ended.ended_ts
    FROM worklogs_ended WHERE span.worklog_id = worklogs_ended.id AND span.ended_ts IS NULL
    """,
    # Worklogs, numbered after the initiative's last, each old name kept.
    """
    WITH moving AS (
        SELECT worklog.id, worklog.sequence, pair.idea, pair.initiative, pair.promoted_ts,
            (SELECT coalesce(max(held.sequence), 0) FROM worklogs held WHERE held.record_number = pair.initiative)
                + row_number() OVER (PARTITION BY pair.idea ORDER BY worklog.sequence) AS moved_sequence
        FROM worklogs worklog JOIN promoted_pairs pair ON worklog.record_number = pair.idea
    ), named AS (
        INSERT INTO worklog_redirects (name, worklog_id, redirected_ts)
        SELECT moving.idea || '.' || moving.sequence, moving.id, moving.promoted_ts FROM moving
    )
    UPDATE worklogs worklog SET record_number = moving.initiative, sequence = moving.moved_sequence
    FROM moving WHERE worklog.id = moving.id
    """,
    # Claims, whose waits follow them by id.
    """
    UPDATE claims claim SET record_number = pair.initiative
    FROM promoted_pairs pair WHERE claim.record_number = pair.idea
    """,
    # Comments on the record. A line comment follows its document through the key's ON UPDATE CASCADE.
    """
    UPDATE comments comment SET record_number = pair.initiative
    FROM promoted_pairs pair WHERE comment.record_number = pair.idea AND comment.document_id IS NULL
    """,
    """
    UPDATE record_checks checked SET record_number = pair.initiative
    FROM promoted_pairs pair WHERE checked.record_number = pair.idea
    """,
    # Documents, each renamed `<name>-from-<idea>` where the initiative holds its name, each old address kept.
    """
    WITH moving AS (
        SELECT document.id, document.name, pair.idea, pair.initiative, pair.promoted_ts,
            CASE WHEN EXISTS (SELECT 1 FROM documents held WHERE held.record_number = pair.initiative AND held.name = document.name)
                THEN document.name || '-from-' || pair.idea ELSE document.name END AS moved_name
        FROM documents document JOIN promoted_pairs pair ON document.record_number = pair.idea
    ), kept AS (
        INSERT INTO document_redirects (record_number, name, document_id, redirected_ts)
        SELECT moving.idea, moving.name, moving.id, moving.promoted_ts FROM moving
    )
    UPDATE documents document SET record_number = moving.initiative, name = moving.moved_name
    FROM moving WHERE document.id = moving.id
    """,
    # Refs, tags and pursuits as a union. An initiative holds pursuits, and the duplicates go with the idea's record.
    """
    UPDATE record_refs link SET record_number = pair.initiative
    FROM promoted_pairs pair
    WHERE link.record_number = pair.idea
      AND NOT EXISTS (SELECT 1 FROM record_refs held WHERE held.record_number = pair.initiative AND held.ref = link.ref)
    """,
    """
    UPDATE record_tags link SET record_number = pair.initiative
    FROM promoted_pairs pair
    WHERE link.record_number = pair.idea
      AND NOT EXISTS (SELECT 1 FROM record_tags held WHERE held.record_number = pair.initiative AND held.tag_id = link.tag_id)
    """,
    """
    UPDATE record_pursuits link SET record_number = pair.initiative
    FROM promoted_pairs pair
    WHERE link.record_number = pair.idea
      AND NOT EXISTS (SELECT 1 FROM record_pursuits held WHERE held.record_number = pair.initiative AND held.pursuit_id = link.pursuit_id)
    """,
    # Each number that led to the idea leads to the initiative, so no redirect forms a chain.
    """
    UPDATE record_redirects redirect SET to_number = pair.initiative
    FROM promoted_pairs pair WHERE redirect.to_number = pair.idea
    """,
    """
    UPDATE initiatives initiative SET idea_id = NULL
    FROM promoted_pairs pair WHERE initiative.number = pair.initiative
    """,
    'DELETE FROM ideas idea USING promoted_pairs pair WHERE idea.number = pair.idea',
    'DELETE FROM records record USING promoted_pairs pair WHERE record.number = pair.idea',
    """
    INSERT INTO record_redirects (
        number, to_number, reason, former_kind, former_id, former_title, former_priority, former_flow_id,
        former_created_ts, redirected_ts, redirected_by
    )
    SELECT pair.idea, pair.initiative, 'promoted', 'idea', pair.former_id, pair.former_title, pair.former_priority,
        pair.former_flow_id, pair.former_created_ts, pair.promoted_ts, coalesce(pair.promoted_by, :actor)
    FROM promoted_pairs pair
    """,
    # A promotion keeps the idea's number, so no initiative names an idea from here on.
    'ALTER TABLE initiatives ADD CONSTRAINT ck_initiatives_initiative_idea_id_unset CHECK (idea_id IS NULL)',
)

# The release before keeps serving, so its writes on a pair wait here and then meet the fold, never the delete's cascade.
LOCK_PAIRS = """
SELECT 1 FROM records record
WHERE record.number IN (SELECT idea FROM promoted_pairs UNION SELECT initiative FROM promoted_pairs)
ORDER BY record.number
FOR UPDATE
"""


def upgrade() -> None:
    connection = op.get_bind()
    connection.execute(sa.text(PAIRS))
    connection.execute(sa.text(LOCK_PAIRS))
    pairs = connection.execute(sa.text(WHAT_MOVES)).mappings().all()
    for pair in pairs:
        logger.info(
            '#%s folds into initiative #%s, promoted %s by %s: %s worklogs, %s claims, %s comments, %s checks, %s documents; '
            'refs %s moved of %s, tags %s moved of %s, pursuits %s moved of %s, the rest already on the initiative',
            pair['idea'],
            pair['initiative'],
            pair['promoted_ts'].isoformat(),
            pair['promoted_by'] or ACTOR,
            pair['worklogs'],
            pair['claims'],
            pair['comments'],
            pair['checks'],
            pair['documents'],
            pair['refs'],
            pair['refs_held'],
            pair['tags'],
            pair['tags_held'],
            pair['pursuits'],
            pair['pursuits_held'],
        )
    connection.execute(sa.select(sa.func.set_config('audit.actor', ACTOR, True)))
    for statement in FOLD:
        connection.execute(sa.text(statement), {'actor': ACTOR})
    connection.execute(sa.text('DROP TABLE promoted_pairs'))
    # The setting lasts the run's transaction, so a later migration in the same run is not signed as this one.
    connection.execute(sa.select(sa.func.set_config('audit.actor', '', True)))
    logger.info('folded %s promoted pairs into their initiatives', len(pairs))


def downgrade() -> None:
    raise RuntimeError(
        'This migration deleted each promoted idea and moved its rows onto its initiative, which nothing can rebuild. '
        'Restore the database backup taken before revision 86158442774b to go back.'
    )
