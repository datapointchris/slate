"""What every kind's read carries beside its own fields, because it hangs off the record's number."""

from slate.schemas.claim import Claim
from slate.schemas.comment import Comment
from slate.schemas.document import DocumentSummary
from slate.schemas.record import RecordModel


class RecordParts(RecordModel):
    """`claims` is every claim the record has had, ended ones included, and `comments` every comment on
    the record itself, each oldest first. `documents` is each of its documents, with its latest version.
    """

    claims: list[Claim] = []
    comments: list[Comment] = []
    documents: list[DocumentSummary] = []
