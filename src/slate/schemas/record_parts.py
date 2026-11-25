"""What every kind's read carries beside its own fields, because it hangs off the record's number."""

import datetime as dt

from slate.models.record_redirect import RedirectReason
from slate.schemas.claim import Claim
from slate.schemas.comment import Comment
from slate.schemas.document import DocumentSummary
from slate.schemas.record import RecordModel


class RedirectedFrom(RecordModel):
    """A retired number that leads to this record, why, what the idea holding it was called, and since when."""

    number: int
    reason: RedirectReason
    former_title: str
    redirected_ts: dt.datetime


class RecordParts(RecordModel):
    """`claims` is every claim the record has had, ended ones included, and `comments` every comment on
    the record itself, each oldest first. `documents` is each of its documents, with its latest version.
    `redirected_from` is each retired number leading here, oldest first, which is where a wrong fold shows.
    A read through one of them answers this record, so its `number` differs from the one asked for.
    """

    claims: list[Claim] = []
    comments: list[Comment] = []
    documents: list[DocumentSummary] = []
    redirected_from: list[RedirectedFrom] = []
