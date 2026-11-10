import datetime as dt
from typing import Any
from typing import Literal

from slate.schemas.record import RecordModel


class RecordChange(RecordModel):
    """One write to the record or to a row hanging off it, as the audit trail stored it.

    `table_name` and `record_id` name the row written, by its table's own key. A row imported from
    another tracker keeps the table name, key and actor it was written with. `changes` maps each
    column the write touched to `{"old": ..., "new": ...}`. An insert carries no `old`, and a delete
    no `new`. `is_own_row` is true for a write to the record's own row, in its kind's table or in
    `records`, and false for a write to a row hanging off it. `traceparent` and `baggage` are the W3C
    headers of the span that wrote it, null for a write made outside a trace. `referenced_names`
    maps each column holding the key of a row a person knows by name, as a tagging's `tag_id`, to
    that name on the same sides as `changes`. A row since deleted reads the name its delete held.
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
    traceparent: str | None
    baggage: str | None
    is_own_row: bool = False
    referenced_names: dict[str, dict[str, str]] = {}
