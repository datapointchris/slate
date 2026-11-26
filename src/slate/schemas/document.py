"""A document's read, the summary a record's read lists, and the request that writes a version."""

import datetime as dt
from typing import Any
from typing import Self

from pydantic import model_validator

from slate.models.document import DocumentKindName
from slate.schemas.comment import NonBlank
from slate.schemas.comment import PlacedComment
from slate.schemas.record import RecordModel
from slate.schemas.record import RecordSummary
from slate.schemas.write_effects import Written


class DocumentVersionSummary(RecordModel):
    """One version: who wrote it, the agent session that did, the worklog that session was in, and the flow it wrote under.

    `fields` is the JSON object written beside its text, null where the write sent none.
    """

    version: int
    written_by: str
    session: str | None = None
    worklog: str | None = None
    written_ts: dt.datetime
    flow_id: str | None = None
    line_count: int
    fields: dict[str, Any] | None = None


class DocumentVersion(DocumentVersionSummary):
    """`lines` is the body as a line comment numbers it, so a client prints the numbers it takes."""

    body: str
    lines: list[str]


class DocumentSummary(RecordModel):
    """A document as a record's read lists it: its latest version, and how many line comments it has."""

    record_number: int
    name: str
    address: str
    kind: str
    created_ts: dt.datetime
    latest: DocumentVersionSummary
    comment_count: int


class ListedDocument(DocumentSummary):
    """A document as `GET /documents` lists it, naming its record, so a list groups documents with no read per record."""

    record: RecordSummary


class DocumentVersionRef(RecordModel):
    """A version a worklog wrote, named by its document's address, with the fields written beside its text."""

    address: str
    kind: str
    version: int
    written_ts: dt.datetime
    fields: dict[str, Any] | None = None


class Document(RecordModel):
    """One version of a document, with every version listed and every line comment placed on it.

    `version` is the version read, the latest unless one was asked for. `comments` holds the line
    comments written on every version, oldest first, each placed on this one.
    """

    record: RecordSummary
    name: str
    address: str
    kind: str
    created_ts: dt.datetime
    version: DocumentVersion
    versions: list[DocumentVersionSummary]
    comments: list[PlacedComment] = []


class DocumentWrite(RecordModel):
    """Write the next version of the document the path names, creating it on its first write.

    `kind` is needed to create one, and must be the document's own when given after. The body is
    kept as given, so its lines number as the writer's file did. `fields` is a JSON object kept
    beside it, which slate stores and never reads. `session` is the agent session writing it, and
    the version names the worklog that session is in.
    """

    body: str
    fields: dict[str, Any] | None = None
    kind: DocumentKindName | None = None
    session: NonBlank | None = None
    host: str | None = None

    @model_validator(mode='after')
    def a_body_holds_something(self) -> Self:
        if not self.body.strip():
            raise ValueError('body is blank; a document version holds text')
        return self


class DocumentWritten(Written):
    document: Document
