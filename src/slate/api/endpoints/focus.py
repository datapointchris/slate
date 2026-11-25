"""The focus: the share of the work each pursuit should get for a while, and where each stands against it. `docs/focus.md` says why."""

from typing import Annotated

import structlog
from fastapi import APIRouter
from fastapi import Query
from fastapi import status

from slate import schemas
from slate.api.callers import CurrentCaller
from slate.api.callers import DbSession
from slate.api.callers import caller_actor
from slate.api.request_zone import RequestZone
from slate.services.focus_standing import focus_standing
from slate.services.focus_usage_by_day import focus_usage_by_day
from slate.services.focuses import end_focus
from slate.services.focuses import focus_history
from slate.services.focuses import focus_view
from slate.services.focuses import set_focus
from slate.services.issue_views import calendar_now
from slate.services.row_limit import RowLimit
from slate.services.write_effects import Effects

logger = structlog.get_logger()
router = APIRouter()


@router.get('', response_model=schemas.FocusStanding, status_code=status.HTTP_200_OK)
def read_standing(session: DbSession, zone: RequestZone):
    """The focus in force, null where none is, and each pursuit's share, usage, factor and last touch against it."""
    _, now = calendar_now(zone)
    return focus_standing(session, now, zone)


@router.get('/usage', response_model=schemas.FocusUsage, status_code=status.HTTP_200_OK)
def read_usage(
    session: DbSession,
    zone: RequestZone,
    focus: Annotated[int | None, Query(description='The id of the focus to read; the one in force where none is named')] = None,
):
    """Each day of a focus, oldest first: each pursuit's output that day, and its usage, share and factor over the window ending with it."""
    _, now = calendar_now(zone)
    return focus_usage_by_day(session, now, zone, focus)


@router.get('/history', response_model=list[schemas.Focus], status_code=status.HTTP_200_OK)
def read_history(session: DbSession, zone: RequestZone, limit: RowLimit = None):
    """Every focus, ended or not, the latest set first."""
    _, now = calendar_now(zone)
    return [focus_view(focus, now, zone) for focus in focus_history(session, limit)]


@router.put('', response_model=schemas.FocusWritten, status_code=status.HTTP_200_OK)
def set_(body: schemas.FocusSet, session: DbSession, caller: CurrentCaller, effects: Effects):
    """Set a focus from now, ending the one in force as replaced."""
    today, now = calendar_now(effects.zone)
    focus = set_focus(session, body, caller_actor(caller), now, today, effects)
    session.commit()
    session.refresh(focus)
    return effects.answer(schemas.FocusWritten, focus=focus_view(focus, now, effects.zone))


@router.post('/end', response_model=schemas.FocusWritten, status_code=status.HTTP_200_OK)
def end(body: schemas.FocusEnd, session: DbSession, effects: Effects):
    """End the focus in force with a reason. With none in force, nothing changes and the answer warns."""
    _, now = calendar_now(effects.zone)
    focus = end_focus(session, body.reason, now, effects)
    session.commit()
    if focus is None:
        return effects.answer(schemas.FocusWritten, focus=None)
    session.refresh(focus)
    return effects.answer(schemas.FocusWritten, focus=focus_view(focus, now, effects.zone))
