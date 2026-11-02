from pydantic import BaseModel

from slate.schemas.record import RecordSummary


class WriteEffect(BaseModel):
    """One thing a write did beyond what it was asked, or one thing it found worth a look.

    `code` is the stable key a client branches on, and `message` the sentence it prints. `record`
    is the number of the record the effect names, where it names one.
    """

    code: str
    message: str
    record: int | None = None


class Written(BaseModel):
    """What every write answers beside what it wrote.

    `side_effects` is every change the write made that its request did not name. `warnings` is
    every conflict the write went through anyway, for the caller to check before carrying on.
    """

    side_effects: list[WriteEffect] = []
    warnings: list[WriteEffect] = []


class RecordDeleted(Written):
    """`deleted` is the record as it stood before the delete took it."""

    deleted: RecordSummary
