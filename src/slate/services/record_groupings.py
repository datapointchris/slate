"""The filters that narrow every kind of record, read from a list's query and applied in one call.

A tag and a direction each narrow ideas, initiatives and issues, an issue through its initiative.
Each list and the queue narrow through `narrowed_to_groupings`, so a grouping added to
`RecordGroupings` and applied here reaches every one of them.
"""

from __future__ import annotations

from typing import Annotated

from fastapi import Depends
from fastapi import Query
from sqlalchemy import Select
from sqlalchemy.orm import Session

from slate.models.record import Kind
from slate.schemas.ready_filter import DIRECTION_FILTER
from slate.schemas.ready_filter import TAG_FILTER
from slate.schemas.ready_filter import RecordGroupings
from slate.services.record_directions import narrowed_to_direction
from slate.services.record_tags import narrowed_to_tag


def narrowed_to_groupings(query: Select, kind: Kind, groupings: RecordGroupings, session: Session) -> Select:
    """Narrow a read of `kind` to the records in every grouping `groupings` names."""
    return narrowed_to_direction(narrowed_to_tag(query, kind, groupings.tag, session), kind, groupings.direction, session)


def groupings_query(
    tag: Annotated[str | None, Query(description=TAG_FILTER)] = None,
    direction: Annotated[int | str | None, Query(description=DIRECTION_FILTER)] = None,
) -> RecordGroupings:
    return RecordGroupings(tag=tag, direction=direction)


GroupingsQuery = Annotated[RecordGroupings, Depends(groupings_query)]
