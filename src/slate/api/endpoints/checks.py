"""The order issues and initiatives come due for a check, across every record."""

from typing import Annotated

from fastapi import APIRouter
from fastapi import Depends
from fastapi import status

from slate import schemas
from slate.api.callers import DbSession
from slate.api.request_zone import RequestZone
from slate.config import Settings
from slate.config import get_settings
from slate.services.check_due import due_checks
from slate.services.row_limit import RowLimit

router = APIRouter()


@router.get('/due', response_model=list[schemas.DueCheck], status_code=status.HTTP_200_OK)
def read_due(
    session: DbSession, zone: RequestZone, settings: Annotated[Settings, Depends(get_settings)], limit: RowLimit = None
) -> list[schemas.DueCheck]:
    """Each open issue and initiative due a check, first due first, with why and its likely duplicates.

    A record a live claim holds is never due. The intervals come from the API's settings, so every
    reader reads the same order.
    """
    return due_checks(session, zone, settings.checks, limit)
