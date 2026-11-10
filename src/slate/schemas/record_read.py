"""The read of any record by its number: the kind that holds it, and that kind's whole read."""

from typing import Annotated
from typing import Literal

from pydantic import BaseModel
from pydantic import Field

from slate.schemas.idea import IdeaDetail
from slate.schemas.initiative import InitiativeDetail
from slate.schemas.issue import IssueDetail
from slate.schemas.pursuit import PursuitDetail


class IssueRecord(BaseModel):
    kind: Literal['issue'] = 'issue'
    number: int
    record: IssueDetail


class InitiativeRecord(BaseModel):
    kind: Literal['initiative'] = 'initiative'
    number: int
    record: InitiativeDetail


class IdeaRecord(BaseModel):
    kind: Literal['idea'] = 'idea'
    number: int
    record: IdeaDetail


class PursuitRecord(BaseModel):
    kind: Literal['pursuit'] = 'pursuit'
    number: int
    record: PursuitDetail


RecordRead = Annotated[IssueRecord | InitiativeRecord | IdeaRecord | PursuitRecord, Field(discriminator='kind')]
