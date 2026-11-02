"""A comment's read, and the requests that add and delete one."""

import datetime as dt
from typing import Annotated
from typing import Self

from pydantic import Field
from pydantic import StringConstraints
from pydantic import model_validator

from slate.schemas.record import RecordModel
from slate.schemas.record import RecordRef
from slate.schemas.write_effects import Written

NonBlank = Annotated[str, StringConstraints(strip_whitespace=True, min_length=1)]
LineNumber = Annotated[int, Field(ge=1, description='Counting from one')]


class CommentLine(RecordModel):
    """The document version and line a line comment was written on, and the line as it read there."""

    document: str
    kind: str
    version: int
    line: int
    text: str


class Comment(RecordModel):
    """A comment on a record, or on one line of its document's version when `on` is set.

    `written_by` is the caller's actor, as the audit trail records it, and null on a comment nobody recorded the
    writer of. `session` and `worklog` are where an agent session wrote it from.
    """

    id: int
    record_number: int
    body: str
    written_by: str | None = None
    session: str | None = None
    worklog: str | None = None
    written_ts: dt.datetime
    on: CommentLine | None = None


class PlacedComment(Comment):
    """A line comment read with a version of its document.

    `shown_line` is the line of the version read that the comment sits on. A comment carries
    through every version that kept its line unchanged, and is null on one that changed or removed it.
    """

    shown_line: int | None = None


class CommentAdd(RecordModel):
    """A comment on `record`, or on `line` of its document `document`.

    `version` defaults to the document's latest. `session` is the agent session writing it, and the
    comment names the worklog that session is in.
    """

    record: RecordRef
    body: NonBlank
    document: NonBlank | None = None
    version: LineNumber | None = None
    line: LineNumber | None = None
    session: NonBlank | None = None
    host: str | None = None

    @model_validator(mode='after')
    def a_line_needs_its_document(self) -> Self:
        if (self.document is None) != (self.line is None):
            raise ValueError('a line comment names both document and line, and a comment on the record names neither')
        if self.version is not None and self.document is None:
            raise ValueError('version names a version of document, so it needs document and line')
        return self


class CommentWritten(Written):
    comment: Comment


class CommentDeleted(Written):
    """`deleted` is the comment as it stood."""

    deleted: Comment
