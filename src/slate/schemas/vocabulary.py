from slate.schemas.issue import IssueLabel
from slate.schemas.record import RecordModel
from slate.schemas.tag import Tag
from slate.schemas.worklog import WorklogStep


class PriorityName(RecordModel):
    value: int
    name: str


class Vocabulary(RecordModel):
    """Every value a closed field accepts, for a client building its pickers.

    A list one noun's field alone takes is named for that noun. Every kind shares the priority scale.
    `worklog_steps` comes in position order, the order a record goes through them. `queue_steps` is
    what a `GET /queue` row asks for: every worklog step, with `discuss` for an idea and `decide` for
    a decision issue, in the order work moves through them. `tags` is what every kind's `--tag` takes.
    """

    issue_statuses: list[str]
    issue_types: list[str]
    priorities: list[PriorityName]
    initiative_statuses: list[str]
    document_kinds: list[str]
    worklog_steps: list[WorklogStep]
    queue_steps: list[str]
    labels: list[IssueLabel]
    tags: list[Tag]
