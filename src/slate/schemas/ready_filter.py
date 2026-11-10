from typing import Self
from uuid import UUID

from pydantic import BaseModel
from pydantic import Field
from pydantic import model_validator

AUTOFIXABLE_FILTER = "True for issues whose own, initiative's and parents' autofix are all on, false for the rest"

TAG_FILTER = "A tag's name: only records carrying it, an issue also through its initiative's"

PURSUIT_FILTER = "A pursuit's number or exact name: only ideas and initiatives in it, and the issues of those initiatives"

DIRECTION_FILTER = '`pursuit` under the name the release before pursuits took. `pursuit` wins where both are set.'


class RecordGroupings(BaseModel):
    """The filters that narrow every kind of record: the tag it carries and the pursuit it is in.

    `narrowed_to_groupings` in `services/record_groupings.py` applies each field, and a queue filter
    not declared here narrows only issues.
    """

    tag: str | None = Field(None, description=TAG_FILTER)
    pursuit: int | str | None = Field(None, description=PURSUIT_FILTER)
    direction: int | str | None = Field(None, description=DIRECTION_FILTER)

    @model_validator(mode='after')
    def direction_narrows_as_pursuit(self) -> Self:
        if self.pursuit is None:
            self.pursuit = self.direction
        return self


class ReadyFilter(RecordGroupings):
    """The filters on ready issues: fields of holding the queue's head, and the base of `GET /queue`'s query."""

    repo: str | None = Field(None, description='A repo name, or an empty string for issues on no repo')
    type: str | None = Field(None, description='An issue type')
    label: str | None = Field(None, description='A label slug')
    initiative: UUID | int | str | None = Field(
        None, description='An initiative, by any reference GET /initiatives/{id} accepts: its UUID, number or name'
    )
    autofixable: bool | None = Field(None, description=AUTOFIXABLE_FILTER)
