from slate.schemas.issue import IssueLabel
from slate.schemas.record import RecordModel


class PriorityName(RecordModel):
    value: int
    name: str


class Vocabulary(RecordModel):
    """Every value a closed field of an issue or an initiative accepts, for a client building its pickers."""

    statuses: list[str]
    types: list[str]
    priorities: list[PriorityName]
    initiative_statuses: list[str]
    labels: list[IssueLabel]
