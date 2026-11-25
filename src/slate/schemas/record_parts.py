"""What every kind's read carries beside its own fields, because it hangs off the record's number."""

import datetime as dt

from slate.models.record_redirect import RedirectReason
from slate.schemas.claim import Claim
from slate.schemas.comment import Comment
from slate.schemas.document import DocumentSummary
from slate.schemas.record import RecordModel


class RedirectedFrom(RecordModel):
    """A retired number that leads to this record, why, what held it, and since when.

    `sentence` says it whole, as `#4086 was folded into initiative #2356 on 2026-11-24`, dated in
    the request's `timezone`, as a refusal through the number says it.
    """

    number: int
    reason: RedirectReason
    former_kind: str
    former_title: str
    redirected_ts: dt.datetime
    sentence: str


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
