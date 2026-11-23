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

# A prefix that a list narrows to: the records carrying a ref starting with it.
REF_PREFIX_FILTER = "A ref prefix, such as 'capture/': only records carrying a ref that starts with it"


class RecordRefsLink(RecordModel):
    """Refs to link, each once. A ref that the record already carries is left as it is, with a warning.

    `linked_by` left out signs each with the caller.
    """

    refs: list[RefText] = Field(min_length=1)
    note: str | None = None
    linked_by: Actor | None = None


class RecordRef(RecordModel):
    """`kind` is derived from the ref: `url` for an http or https URL, `key` for anything else."""

    ref: str
    kind: RefKind
    note: str | None = None
    linked_by: str | None = None
    linked_ts: dt.datetime
