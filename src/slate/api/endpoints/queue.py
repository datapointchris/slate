"""The queue: every kind of open work in the order to take it, read by `slate next`."""

from typing import Annotated

from fastapi import APIRouter
from fastapi import Query
from fastapi import status

from slate import schemas
from slate.api.callers import DbSession
from slate.api.request_zone import RequestZone
from slate.services.row_limit import RowLimit
from slate.services.work_queue import read_queue

router = APIRouter()


@router.get('', response_model=schemas.Queue, status_code=status.HTTP_200_OK)
def read(session: DbSession, zone: RequestZone, filters: Annotated[schemas.QueueFilter, Query()], limit: RowLimit = None):
    """Ideas, initiatives and issues by priority, each with its step; the first row of `queue` is next.

    Within a priority each initiative's issues run together, in the order its rank sets. Held work
    is listed apart in `claimed`, by holder. `limit` caps `queue` and never `claimed`.
    """
    return read_queue(session, zone, filters, limit)
