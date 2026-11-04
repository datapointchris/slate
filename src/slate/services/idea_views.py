"""Build the idea read shape: the idea, its evidence, its claim, and the initiative it became."""

from __future__ import annotations

import datetime as dt
from collections.abc import Sequence
from urllib.parse import urlsplit

from sqlalchemy.orm import Session
from sqlalchemy.orm import selectinload

from slate import models
from slate import schemas
from slate.schemas.idea import EvidenceKind
from slate.services.claim_views import unended_claim_views
from slate.services.initiative_views import initiatives_of_ideas
from slate.services.record_parts import record_parts

IDEA_LOAD_OPTIONS = (selectinload(models.Idea.evidence),)


def evidence_kind(ref: str) -> EvidenceKind:
    parts = urlsplit(ref)
    return 'url' if parts.scheme in ('http', 'https') and parts.netloc else 'key'


def evidence_view(evidence: models.IdeaEvidence) -> schemas.IdeaEvidence:
    return schemas.IdeaEvidence(
        ref=evidence.ref,
        kind=evidence_kind(evidence.ref),
        note=evidence.note,
        linked_by=evidence.linked_by,
        linked_ts=evidence.linked_ts,
    )


def idea_views(session: Session, ideas: Sequence[models.Idea]) -> list[schemas.Idea]:
    initiatives = initiatives_of_ideas(session, [idea.id for idea in ideas])
    claims = unended_claim_views(session, [idea.number for idea in ideas], dt.datetime.now(dt.UTC))
    return [
        schemas.Idea(
            id=idea.id,
            number=idea.number,
            title=idea.title,
            description=idea.description,
            priority=idea.priority,
            rank=idea.rank,
            pinned_ts=idea.pinned_ts,
            pinned_by=idea.pinned_by,
            pinned_session=idea.pinned_session,
            created_ts=idea.created_ts,
            updated_ts=idea.updated_ts,
            evidence=[evidence_view(evidence) for evidence in idea.evidence],
            initiative=initiatives.get(idea.id),
            claim=claims.get(idea.number),
        )
        for idea in ideas
    ]


def idea_view(session: Session, idea: models.Idea) -> schemas.Idea:
    return idea_views(session, [idea])[0]


def idea_detail(session: Session, idea: models.Idea) -> schemas.IdeaDetail:
    return schemas.IdeaDetail(
        **idea_view(session, idea).model_dump(),
        **record_parts(session, idea.number, dt.datetime.now(dt.UTC)).model_dump(),
    )
