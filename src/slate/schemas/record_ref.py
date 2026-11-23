import datetime as dt
from typing import Annotated
from typing import Literal

from pydantic import Field
from pydantic import StringConstraints

from slate.schemas.record import Actor
from slate.schemas.record import RecordModel

# A key that the system holding the thing gave it, such as an inbox row's `capture/<id>`, or a URL.
# slate stores it as given and never follows it.
RefText = Annotated[str, StringConstraints(strip_whitespace=True, min_length=1, max_length=2048)]

RefKind = Literal['url', 'key']


class RecordRefsLink(RecordModel):
    """Refs to link, each once. A ref that the record already carries is left as it is, with a warning.

    `linked_by` left out signs each with the caller.
    """

    refs: list[RefText] = Field(min_length=1)
    note: str | None = None
    linked_by: Actor | None = None


class RecordRef(RecordModel):
    """`kind` is `url` for an http or https URL with a host, `key` for anything else."""

    ref: str
    kind: RefKind
    note: str | None = None
    linked_by: str | None = None
    linked_ts: dt.datetime
