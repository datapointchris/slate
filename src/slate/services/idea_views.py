"""Build the idea read shape: the idea, its refs, its claim, and the initiative it became."""

from __future__ import annotations

import datetime as dt
from collections.abc import Sequence

from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.services.claim_views import unended_claim_views
from slate.services.idea_promotion import PromotedIdea
from slate.services.initiative_views import initiatives_of_ideas
from slate.services.record_parts import record_parts
from slate.services.record_pursuits import pursuits_of
from slate.services.record_refs import refs_of
from slate.services.record_tags import own_tags


def idea_views(session: Session, ideas: Sequence[models.Idea]) -> list[schemas.Idea]:
    initiatives = initiatives_of_ideas(session, [idea.id for idea in ideas])
    claims = unended_claim_views(session, [idea.number for idea in ideas], dt.datetime.now(dt.UTC))
    tags = own_tags(session, [idea.number for idea in ideas])
    pursuits = pursuits_of(session, [idea.number for idea in ideas])
    refs = refs_of(session, [idea.number for idea in ideas])
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
            flow_id=idea.flow_id,
            created_ts=idea.created_ts,
            updated_ts=idea.updated_ts,
            tags=tags.get(idea.number, []),
            pursuits=pursuits.get(idea.number, []),
            refs=refs.get(idea.number, []),
            initiative=initiatives.get(idea.id),
            claim=claims.get(idea.number),
        )
        for idea in ideas
    ]


def idea_view(session: Session, idea: models.Idea) -> schemas.Idea:
    return idea_views(session, [idea])[0]


def promoted_idea_view(promoted: PromotedIdea, initiative: schemas.Initiative, now: dt.datetime) -> schemas.Idea:
    """The idea as the release before in-place promotion answered a promotion, naming the initiative it became.

    The initiative now holds the number's parts, so the view reads them from it.
    """
    return schemas.Idea(
        id=promoted.id,
        number=initiative.number,
        title=promoted.title,
        description=promoted.description,
        priority=promoted.priority,
        rank=initiative.rank,
        pinned_ts=initiative.pinned_ts,
        pinned_by=initiative.pinned_by,
        pinned_session=initiative.pinned_session,
        flow_id=initiative.flow_id,
        created_ts=promoted.created_ts,
        updated_ts=now,
        tags=initiative.tags,
        pursuits=initiative.pursuits,
        refs=initiative.refs,
        initiative=initiative,
    )


def idea_detail(session: Session, idea: models.Idea) -> schemas.IdeaDetail:
    return schemas.IdeaDetail(
        **idea_view(session, idea).model_dump(),
        **record_parts(session, idea.number, dt.datetime.now(dt.UTC)).model_dump(),
    )
