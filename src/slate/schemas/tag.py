import datetime as dt
from typing import Annotated

from pydantic import BaseModel
from pydantic import ConfigDict
from pydantic import StringConstraints

from slate.models.tag import TAG_NAME_PATTERN
from slate.schemas.not_null import NotNull
from slate.schemas.record import RecordModel
from slate.schemas.write_effects import Written

TAG_DESCRIPTION_CHARS = 240

TagName = Annotated[str, StringConstraints(pattern=TAG_NAME_PATTERN)]

TagDescription = Annotated[
    str, StringConstraints(strip_whitespace=True, min_length=1, max_length=TAG_DESCRIPTION_CHARS, pattern=r'^[^\r\n]+$')
]


class TagCreate(BaseModel):
    """A tag and when it applies, on one line. A misspelled field is refused rather than dropped."""

    model_config = ConfigDict(extra='forbid')

    name: TagName
    description: TagDescription


class TagUpdate(BaseModel):
    """A rename, a new description, or both. Every record carrying the tag keeps it under the new name."""

    model_config = ConfigDict(extra='forbid')

    name: NotNull[TagName] = None
    description: NotNull[TagDescription] = None


class Tag(RecordModel):
    """A tag with how many open records carry it themselves, in all and by kind.

    An issue reading its initiative's tag is not counted, because the initiative is.
    """

    name: str
    description: str
    created_ts: dt.datetime
    open_record_count: int = 0
    open_idea_count: int = 0
    open_initiative_count: int = 0
    open_issue_count: int = 0


class TagWritten(Written):
    tag: Tag


class TagDeleted(Written):
    """`deleted` is the tag as it stood, its counts the open records that carried it."""

    deleted: Tag
