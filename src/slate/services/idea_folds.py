"""Fold an idea into the work that covers it: every row moves to the target, and the idea's number leads there.

This is a CRM's merge. Salesforce reparents the loser's related records onto the survivor, and
HubSpot answers the loser's old id with the survivor. The target keeps its own number, fields and
place in the queue. The idea's `ideas` and `records` rows go, and a `record_redirects` row leads
its number, and its `id`, to the target. Each worklog moves under the target's number, after the
target's last, and a `worklog_redirects` row keeps its old name. A fold has no undo: a wrong one
shows in the target's `redirected_from`, and the audit trail keeps the idea's row whole.

`fold_rows_into` moves the rows and writes the redirect, apart from the claims and the answer, so
a migration folding pairs made before the promotion kept the number runs the same steps.
"""

from __future__ import annotations

import datetime as dt
from dataclasses import dataclass
from dataclasses import field
from typing import Any

import structlog
from fastapi import HTTPException
from fastapi import status
from sqlalchemy import Delete
from sqlalchemy import Update
from sqlalchemy import delete
from sqlalchemy import func
from sqlalchemy import insert
from sqlalchemy import select
from sqlalchemy import update
from sqlalchemy.orm import Session

from slate import models
from slate.api.exceptions import NotFoundException
from slate.api.exceptions import with_article
from slate.models.record import Kind
from slate.models.record_redirect import RedirectReason
from slate.schemas.pursuit import PursuitSummary
from slate.services.claim_views import unended_claims
from slate.services.claims import ClaimRefused
from slate.services.claims import end_claim
from slate.services.idea_promotion import idea_number
from slate.services.record_lookup import resolve_record
from slate.services.record_pursuits import pursuits_of
from slate.services.write_effects import WriteEffectCode
from slate.services.write_effects import WriteEffects

logger = structlog.get_logger()

# The kinds whose records hold pursuits of their own. An issue reads its initiative's.
HOLD_PURSUITS = frozenset({Kind.IDEA, Kind.INITIATIVE})


@dataclass
class FoldedRows:
    """What `fold_rows_into` moved, renamed, merged or dropped, so its caller names each."""

    idea_number: int
    target_number: int
    worklogs: list[tuple[str, str]] = field(default_factory=list)
    claims: int = 0
    waits: int = 0
    comments: int = 0
    checks: int = 0
    documents: list[tuple[str, str]] = field(default_factory=list)
    refs: int = 0
    tags: int = 0
    pursuits: int = 0
    pursuits_dropped: list[PursuitSummary] = field(default_factory=list)
    redirects_retargeted: list[int] = field(default_factory=list)
    pinned: bool = False


def lock_fold(session: Session, idea_ref: str, into_ref: int | str) -> tuple[models.Idea, models.Record]:
    """The idea `idea_ref` names and the record `into_ref` names, each `records` row locked `FOR UPDATE`.

    The lower number is locked first, so two folds crossing the same pair wait rather than deadlock.
    A non-idea, a pursuit target and the idea itself each answer 409, and so does a number a fold
    already retired, through the redirect it follows in a write.
    """
    number = idea_number(session, idea_ref)
    target_number = resolve_record(session, into_ref).number
    locked: dict[int, models.Record | None] = {}
    for each in sorted({number, target_number}):
        query = select(models.Record).where(models.Record.number == each).with_for_update().execution_options(populate_existing=True)
        locked[each] = session.scalar(query)
    record = locked[number]
    if record is None:
        raise NotFoundException('idea', idea_ref, logger)
    if record.kind != Kind.IDEA:
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=f'#{number} is {with_article(record.kind)}; only an idea folds')
    target = locked[target_number]
    if target is None:
        raise NotFoundException('record', str(into_ref), logger)
    if target.kind == Kind.PURSUIT:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'#{target_number} is a pursuit; an idea folds into an idea, initiative or issue',
        )
    if target_number == number:
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail='an idea cannot fold into itself')
    return session.scalars(select(models.Idea).where(models.Idea.number == number)).one(), target


def settle_claims(session: Session, idea: int, target: int, holder: str, now: dt.datetime, effects: WriteEffects) -> None:
    """Settle both records' claims before any row moves, so the fold never takes a record from under another session.

    Another holder's live claim on either refuses the fold. An unended claim whose lease ran out ends
    as expired, as a takeover ends one. Where `holder` holds both, its claim on the idea and that
    claim's worklog end as a release ends them, and its claim on the target stays. Otherwise its claim
    on the idea moves to the target with every other row.
    """
    claims = unended_claims(session, [idea, target])
    for number, claim in sorted(claims.items()):
        if claim.is_live(now) and claim.holder != holder:
            raise ClaimRefused(
                f'{claim.holder} holds #{number} until {effects.moment(claim.expires_ts)}; it ends or releases its claim first'
            )
    for number, claim in sorted(claims.items()):
        if claim.is_expired(now):
            effects.did(
                WriteEffectCode.EXPIRED_CLAIM_ENDED,
                f"Ended {claim.holder}'s claim on #{number} as expired at {effects.moment(claim.expires_ts)}, before the fold.",
                number,
            )
            end_claim(claim, 'expired', now, None, effects)
    on_idea, on_target = claims.get(idea), claims.get(target)
    if on_idea is not None and on_idea.ended_ts is None and on_target is not None and on_target.ended_ts is None:
        effects.did(
            WriteEffectCode.CLAIM_ENDED,
            f"Released {holder}'s claim on #{idea}, because it holds #{target}, where #{idea}'s rows go.",
            idea,
        )
        end_claim(on_idea, 'released', now, holder, effects)
    elif on_idea is not None and on_idea.ended_ts is None:
        effects.did(WriteEffectCode.CLAIM_MOVED, f"Moved {holder}'s claim on #{idea} to #{target}, which it now holds.", target)
    session.flush()


def fold_rows_into(
    session: Session,
    idea: int,
    target: int,
    *,
    reason: RedirectReason,
    redirected_ts: dt.datetime,
    redirected_by: str,
) -> FoldedRows:
    """Move every row on idea number `idea` to record `target`, retire the idea, and lead its number there.

    In order: worklogs, renumbered after the target's last and each old name kept; claims, whose
    waits follow them; comments on the record, and checks; documents, each renamed
    `<name>-from-<idea>` where the target already holds its name, their line comments following
    through `ON UPDATE CASCADE`; refs,
    tags and pursuits as a union, or pursuits dropped where the target is an issue. Each redirect
    that led to the idea then leads to the target, so none forms a chain. Last, the `ideas` and
    `records` rows go, which takes the duplicates the unions left, and the redirect is inserted.

    Both `records` rows must already be locked, and no other session's live claim may sit on
    either. Every statement is Core SQL on the tables, so the ORM objects the session holds are
    expired at the end.
    """
    folded = FoldedRows(idea, target)
    folded.pinned = session.scalar(select(models.Record.pinned_ts.is_not(None)).where(models.Record.number == idea)) or False

    last = session.scalar(select(func.coalesce(func.max(models.Worklog.sequence), 0)).where(models.Worklog.record_number == target)) or 0
    moving = session.execute(
        select(models.Worklog.id, models.Worklog.sequence).where(models.Worklog.record_number == idea).order_by(models.Worklog.sequence)
    ).all()
    for offset, (worklog_id, sequence) in enumerate(moving, start=1):
        run(session, update(models.Worklog).where(models.Worklog.id == worklog_id).values(record_number=target, sequence=last + offset))
        session.execute(
            insert(models.WorklogRedirect).values(name=f'{idea}.{sequence}', worklog_id=worklog_id, redirected_ts=redirected_ts)
        )
        folded.worklogs.append((f'{idea}.{sequence}', f'{target}.{last + offset}'))

    claim_ids = list(session.scalars(select(models.Claim.id).where(models.Claim.record_number == idea)))
    folded.claims = len(claim_ids)
    folded.waits = session.scalar(select(func.count()).select_from(models.ClaimWait).where(models.ClaimWait.claim_id.in_(claim_ids))) or 0
    run(session, update(models.Claim).where(models.Claim.record_number == idea).values(record_number=target))

    on_record = (models.Comment.record_number == idea, models.Comment.document_id.is_(None))
    folded.comments = len(
        returned(session, update(models.Comment).where(*on_record).values(record_number=target).returning(models.Comment.id))
    )
    checks = update(models.RecordCheck).where(models.RecordCheck.record_number == idea).values(record_number=target)
    folded.checks = len(returned(session, checks.returning(models.RecordCheck.id)))

    held = set(session.scalars(select(models.Document.name).where(models.Document.record_number == target)))
    documents = session.execute(
        select(models.Document.id, models.Document.name).where(models.Document.record_number == idea).order_by(models.Document.name)
    )
    for document_id, name in documents.all():
        moved_name = f'{name}-from-{idea}' if name in held else name
        run(session, update(models.Document).where(models.Document.id == document_id).values(record_number=target, name=moved_name))
        folded.documents.append((f'{idea}/{name}', f'{target}/{moved_name}'))

    folded.refs = union_into(session, models.RecordRef, models.RecordRef.ref, idea, target)
    folded.tags = union_into(session, models.RecordTag, models.RecordTag.tag_id, idea, target)
    target_kind = session.scalar(select(models.Record.kind).where(models.Record.number == target))
    if target_kind in HOLD_PURSUITS:
        folded.pursuits = union_into(session, models.RecordPursuit, models.RecordPursuit.pursuit_id, idea, target)
    else:
        folded.pursuits_dropped = pursuits_of(session, [idea]).get(idea, [])

    retargeted = update(models.RecordRedirect).where(models.RecordRedirect.to_number == idea).values(to_number=target)
    folded.redirects_retargeted = sorted(returned(session, retargeted.returning(models.RecordRedirect.number)))

    former = session.execute(
        select(models.Idea.id, models.Idea.title, models.Idea.priority, models.Idea.created_ts, models.Record.flow_id)
        .join(models.Record, models.Record.number == models.Idea.number)
        .where(models.Idea.number == idea)
    ).one()
    run(session, delete(models.Idea).where(models.Idea.number == idea))
    run(session, delete(models.Record).where(models.Record.number == idea))
    session.execute(
        insert(models.RecordRedirect).values(
            number=idea,
            to_number=target,
            reason=reason,
            former_kind=Kind.IDEA,
            former_id=former.id,
            former_title=former.title,
            former_priority=former.priority,
            former_flow_id=former.flow_id,
            former_created_ts=former.created_ts,
            redirected_ts=redirected_ts,
            redirected_by=redirected_by,
        )
    )
    session.expire_all()
    logger.info('idea_folded', idea=idea, target=target, reason=reason, worklogs=len(folded.worklogs), claims=folded.claims)
    return folded


def union_into(
    session: Session, model: type[models.RecordRef | models.RecordTag | models.RecordPursuit], key, idea: int, target: int
) -> int:
    """Move each of the idea's link rows that the target lacks, and answer how many moved. The rest go with the idea's record."""
    carried = select(key).where(model.record_number == target)
    moved = update(model).where(model.record_number == idea, key.not_in(carried)).values(record_number=target)
    return len(returned(session, moved.returning(model.record_number)))


def run(session: Session, statement: Update | Delete) -> None:
    """Execute a Core write and leave the ORM's loaded objects alone, because `fold_rows_into` expires every one once it ends."""
    session.execute(statement.execution_options(synchronize_session=False))


def returned(session: Session, statement: Update | Delete) -> list[Any]:
    """Execute a Core write that returns one column, as `run` does, and answer the values it returned."""
    return list(session.scalars(statement.execution_options(synchronize_session=False)))


def name_folded_rows(folded: FoldedRows, effects: WriteEffects) -> None:
    """Name each row the fold moved, renamed, merged or dropped, and where the idea's number now leads."""
    idea, target = folded.idea_number, folded.target_number
    for old, new in folded.worklogs:
        effects.did(WriteEffectCode.WORKLOG_MOVED, f'Moved worklog {old} to #{target} as {new}.', target)
    if folded.claims:
        waits = f', with {counted(folded.waits, "wait")}' if folded.waits else ''
        effects.did(WriteEffectCode.CLAIMS_MOVED, f'Moved {counted(folded.claims, "claim")}{waits} to #{target}.', target)
    if folded.comments:
        effects.did(WriteEffectCode.COMMENTS_MOVED, f'Moved {counted(folded.comments, "comment")} to #{target}.', target)
    if folded.checks:
        effects.did(WriteEffectCode.CHECKS_MOVED, f'Moved {counted(folded.checks, "check")} to #{target}.', target)
    for old, new in folded.documents:
        name = old.partition('/')[2]
        renamed = f', because #{target} holds a {name}' if new.partition('/')[2] != name else ''
        effects.did(WriteEffectCode.DOCUMENT_MOVED, f'Moved document {old} to #{target} as {new}{renamed}.', target)
    links = (
        (WriteEffectCode.REFS_MOVED, folded.refs, 'ref'),
        (WriteEffectCode.TAGS_MOVED, folded.tags, 'tag'),
        (WriteEffectCode.PURSUITS_MOVED, folded.pursuits, 'pursuit'),
    )
    for code, count, noun in links:
        if count:
            effects.did(code, f'Moved {counted(count, noun)} to #{target} that it did not already carry.', target)
    for number in folded.redirects_retargeted:
        effects.did(WriteEffectCode.REDIRECT_RETARGETED, f'#{number} now leads to #{target}, as #{idea} does.', target)
    effects.did(WriteEffectCode.RECORD_REDIRECTED, f'#{idea} now leads to #{target}.', target)
    for pursuit in folded.pursuits_dropped:
        effects.warn(
            WriteEffectCode.PURSUITS_DROPPED,
            f"Dropped pursuit #{pursuit.number} {pursuit.name}, because an issue takes its initiative's pursuits.",
            pursuit.number,
        )
    if folded.pinned:
        effects.warn(WriteEffectCode.PIN_DROPPED, f"Dropped #{idea}'s pin; #{target} keeps its own place in the queue.", target)


def counted(count: int, noun: str) -> str:
    return f'{count} {noun}' if count == 1 else f'{count} {noun}s'
