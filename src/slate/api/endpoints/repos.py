"""The repos the issues name, read as one board. A repo is a name on an issue rather than a record, so no route reads one."""

from fastapi import APIRouter
from fastapi import status

from slate import schemas
from slate.api.callers import DbSession
from slate.api.request_zone import RequestZone
from slate.services.repo_board import repo_board

router = APIRouter()


@router.get('/board', response_model=schemas.RepoBoard, status_code=status.HTTP_200_OK)
def read_board(session: DbSession, zone: RequestZone):
    """Each repo's issues counted by state, its closes per day and per week, and its ready issues longest waiting first."""
    return repo_board(session, zone)
