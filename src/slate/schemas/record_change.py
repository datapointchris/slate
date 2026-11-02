import datetime as dt
from typing import Any
from typing import Literal

from slate.schemas.record import RecordModel


class RecordChange(RecordModel):
    """One write to the record or to a row hanging off it, as the audit trail stored it.

    `table_name` and `record_id` name the row written, by its table's own key. A row imported from
    another tracker keeps the table name, key and actor it was written with. `changes` maps each
    column the write touched to `{"old": ..., "new": ...}`. An insert carries no `old`, and a delete
    no `new`.
    """

    id: int
    changed_ts: dt.datetime
    table_name: str
    record_id: str
    operation: Literal['insert', 'update', 'delete']
    changes: dict[str, Any]
    actor: str | None
    request_id: str | None
    job_run_id: str | None
