"""Read what every kind's read carries beside its own fields, so each kind fills it the same way."""

from __future__ import annotations

import datetime as dt

from sqlalchemy.orm import Session

from slate import schemas
from slate.services.claim_views import claim_history
from slate.services.comment_views import record_comments
from slate.services.document_views import record_documents


def record_parts(session: Session, number: int, now: dt.datetime) -> schemas.RecordParts:
    return schemas.RecordParts(
        claims=claim_history(session, number, now),
        comments=record_comments(session, number),
        documents=record_documents(session, number),
    )
