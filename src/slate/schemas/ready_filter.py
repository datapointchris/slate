from uuid import UUID

from pydantic import BaseModel
from pydantic import Field

AUTOFIXABLE_FILTER = "True for issues whose own, initiative's and parents' autofix are all on, false for the rest"

TAG_FILTER = "A tag's name: only records carrying it, an issue also through its initiative's"

DIRECTION_FILTER = "A direction's number or exact name: only ideas and initiatives in it, and the issues of those initiatives"


class ReadyFilter(BaseModel):
    """The filters on ready issues: fields of holding the queue's head, and the base of `GET /queue`'s query."""

    repo: str | None = Field(None, description='A repo name, or an empty string for issues on no repo')
    type: str | None = Field(None, description='An issue type')
    label: str | None = Field(None, description='A label slug')
    initiative: UUID | int | str | None = Field(
        None, description='An initiative, by any reference GET /initiatives/{id} accepts: its UUID, number or name'
    )
    autofixable: bool | None = Field(None, description=AUTOFIXABLE_FILTER)
    tag: str | None = Field(None, description=TAG_FILTER)
    direction: int | str | None = Field(None, description=DIRECTION_FILTER)
