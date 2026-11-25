"""Read what every kind's read carries beside its own fields, so each kind fills it the same way."""

from __future__ import annotations

import datetime as dt

from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.services.claim_views import claim_history
from slate.services.comment_views import record_comments
from slate.services.document_views import record_documents


def record_parts(session: Session, number: int, now: dt.datetime) -> schemas.RecordParts:
    return schemas.RecordParts(
        claims=claim_history(session, number, now),
        comments=record_comments(session, number),
        documents=record_documents(session, number),
        redirected_from=redirected_from(session, number),
    )


def redirected_from(session: Session, number: int) -> list[schemas.RedirectedFrom]:
    """Each retired number leading to `number`, oldest first."""
    query = select(models.RecordRedirect).where(models.RecordRedirect.to_number == number)
    redirects = session.scalars(query.order_by(models.RecordRedirect.redirected_ts, models.RecordRedirect.number))
    return [schemas.RedirectedFrom.model_validate(redirect) for redirect in redirects]
