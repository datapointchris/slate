from slate.schemas.issue import IssueLabel
from slate.schemas.record import RecordModel


class PriorityName(RecordModel):
    value: int
    name: str


class Vocabulary(RecordModel):
    """Every value a closed field accepts, for a client building its pickers.

    A list one noun's field alone takes is named for that noun. Every kind shares the priority scale.
    """

    issue_statuses: list[str]
    issue_types: list[str]
    priorities: list[PriorityName]
    initiative_statuses: list[str]
    document_kinds: list[str]
    worklog_workflows: list[str]
    labels: list[IssueLabel]
