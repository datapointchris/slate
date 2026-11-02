from uuid import UUID

from pydantic import BaseModel
from pydantic import Field

AUTOFIXABLE_FILTER = "True for issues whose own, initiative's and parents' autofix are all on, false for the rest"


class ReadyFilter(BaseModel):
    """The filters on ready issues: fields of holding the queue's head, and the base of `GET /queue`'s query."""

    repo: str | None = Field(None, description='A repo name, or an empty string for issues on no repo')
    type: str | None = Field(None, description='An issue type. Decisions are left out unless asked for here.')
    label: str | None = Field(None, description='A label slug')
    initiative: UUID | int | str | None = Field(
        None, description='An initiative, by any reference GET /initiatives/{id} accepts: its UUID, number or name'
    )
    autofixable: bool | None = Field(None, description=AUTOFIXABLE_FILTER)
