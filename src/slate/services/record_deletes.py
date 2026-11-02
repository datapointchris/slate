"""Name everything a record's delete takes or changes, read before the delete runs.

Deleting a record cascades to its claims, worklogs, documents, comments and edges, and clears every
other record's link to it.
None of that is in the request, so each is named in the answer.
"""

from __future__ import annotations

import datetime as dt

from sqlalchemy import func
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate.services.claim_views import held_by
from slate.services.write_effects import WriteEffectCode
from slate.services.write_effects import WriteEffects


def name_claims_deleted(session: Session, number: int, deleted_by: str, now: dt.datetime, effects: WriteEffects) -> None:
    """Name the claims the record takes with it, warning when another holder's live claim is one."""
    claims = session.scalars(select(models.Claim).where(models.Claim.record_number == number)).all()
    if not claims:
        return
    live = next((claim for claim in claims if claim.is_live(now)), None)
    if live is not None and live.holder != deleted_by:
        effects.warn(
            WriteEffectCode.DELETED_ANOTHERS_CLAIM,
            f'#{number} was claimed by {held_by(live, effects)}, and the claim went with it.',
            number,
        )
    noun = 'claim' if len(claims) == 1 else 'claims'
    effects.did(WriteEffectCode.CLAIMS_DELETED, f'Deleted the {len(claims)} {noun} #{number} had, its whole claim history.', number)


def name_worklogs_deleted(session: Session, number: int, effects: WriteEffects) -> None:
    """Name the worklogs the record takes with it, and with them their spans and links."""
    sequences = session.scalars(
        select(models.Worklog.sequence).where(models.Worklog.record_number == number).order_by(models.Worklog.sequence)
    ).all()
    if not sequences:
        return
    names = ', '.join(f'{number}.{sequence}' for sequence in sequences)
    noun = 'worklog' if len(sequences) == 1 else 'worklogs'
    effects.did(WriteEffectCode.WORKLOGS_DELETED, f'Deleted {noun} {names}, with their spans and links.', number)


def name_documents_and_comments_deleted(session: Session, number: int, effects: WriteEffects) -> None:
    """Name the documents the record takes with it, with their versions, and the comments, line comments included."""
    names = session.scalars(
        select(models.Document.name).where(models.Document.record_number == number).order_by(models.Document.name)
    ).all()
    if names:
        listed = ', '.join(f'{number}/{name}' for name in names)
        noun = 'document' if len(names) == 1 else 'documents'
        effects.did(WriteEffectCode.DOCUMENTS_DELETED, f'Deleted {noun} {listed}, with every version.', number)
    comments = session.scalar(select(func.count()).select_from(models.Comment).where(models.Comment.record_number == number)) or 0
    if comments:
        noun = 'comment' if comments == 1 else 'comments'
        effects.did(WriteEffectCode.COMMENTS_DELETED, f'Deleted the {comments} {noun} on #{number} and its documents.', number)


def name_record_parts_deleted(session: Session, number: int, deleted_by: str, now: dt.datetime, effects: WriteEffects) -> None:
    """Name what hangs off the record's number and goes with it, whatever the kind."""
    name_claims_deleted(session, number, deleted_by, now, effects)
    name_worklogs_deleted(session, number, effects)
    name_documents_and_comments_deleted(session, number, effects)


def name_issue_delete(session: Session, issue: models.Issue, deleted_by: str, now: dt.datetime, effects: WriteEffects) -> None:
    name_record_parts_deleted(session, issue.number, deleted_by, now, effects)
    children = session.scalars(select(models.Issue.number).where(models.Issue.parent_id == issue.id).order_by(models.Issue.number))
    for child in children:
        effects.did(WriteEffectCode.CHILD_DETACHED, f'#{child} had #{issue.number} as its parent, and has none now.', child)
    dependents = session.scalars(
        select(models.Issue.number)
        .join(models.IssueDependency, models.IssueDependency.issue_id == models.Issue.id)
        .where(models.IssueDependency.depends_on_id == issue.id)
        .order_by(models.Issue.number)
    )
    for dependent in dependents:
        effects.did(WriteEffectCode.DEPENDENCY_REMOVED, f'#{dependent} no longer depends on #{issue.number}.', dependent)


def name_initiative_delete(
    session: Session, initiative: models.Initiative, deleted_by: str, now: dt.datetime, effects: WriteEffects
) -> None:
    name_record_parts_deleted(session, initiative.number, deleted_by, now, effects)
    issues = session.scalars(select(models.Issue.number).where(models.Issue.initiative_id == initiative.id).order_by(models.Issue.number))
    for issue in issues:
        effects.did(
            WriteEffectCode.ISSUE_DETACHED,
            f'#{issue} left initiative #{initiative.number} {initiative.name!r}, and no longer takes its priority.',
            issue,
        )
