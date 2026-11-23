"""The filters that narrow every kind of record, read from a list's query and applied in one call.

A tag and a pursuit each narrow ideas, initiatives and issues, an issue through its initiative. A
ref prefix narrows each record by its own refs. Each list and the queue narrow through
`narrowed_to_groupings`, so a grouping added to `RecordGroupings` and applied here reaches every
one of them.
"""

from __future__ import annotations

from typing import Annotated

from fastapi import Query
from sqlalchemy import Select
from sqlalchemy.orm import Session

from slate.models.record import Kind
from slate.schemas.ready_filter import RecordGroupings
from slate.services.record_pursuits import narrowed_to_pursuit
from slate.services.record_refs import narrowed_to_ref_prefix
from slate.services.record_tags import narrowed_to_tag


def narrowed_to_groupings(query: Select, kind: Kind, groupings: RecordGroupings, session: Session) -> Select:
    """Narrow a read of `kind` to the records in every grouping `groupings` names."""
    query = narrowed_to_pursuit(narrowed_to_tag(query, kind, groupings.tag, session), kind, groupings.pursuit, session)
    return narrowed_to_ref_prefix(query, kind, groupings.ref)


# Each list reads the groupings from its query by the model's own fields, as `GET /queue` reads `QueueFilter`.
GroupingsQuery = Annotated[RecordGroupings, Query()]
