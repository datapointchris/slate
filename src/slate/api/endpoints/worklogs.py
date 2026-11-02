"""Worklogs, each one go at one record: started, joined, linked, edited and finished here, and read.

A hold opens a worklog through the claims routes. These open one with no claim, move a session into
one, and record what one produced. `GET /worklogs/current` is the call a commit hook makes to stamp a
commit with the worklog its session is in.

A scoped client acts only under its own client id, and finishes only a worklog it opened.
"""

import datetime as dt
from typing import Annotated
from typing import Literal

import structlog
from fastapi import APIRouter
from fastapi import Depends
from fastapi import HTTPException
from fastapi import Query
from fastapi import status
from sqlalchemy import or_
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.api.callers import CurrentCaller
from slate.api.callers import DbSession
from slate.api.callers import refuse_acting_for_another
from slate.api.exceptions import ForbiddenException
from slate.api.exceptions import Refusal
from slate.api.oidc_auth import ScopedClient
from slate.services.record_refs import resolve_record
from slate.services.row_limit import RowLimit
from slate.services.row_limit import apply_row_limit
from slate.services.worklog_views import WORKLOG_LOAD_OPTIONS
from slate.services.worklog_views import current_worklog
from slate.services.worklog_views import worklog_state
from slate.services.worklog_views import worklog_views
from slate.services.worklogs import finish
from slate.services.worklogs import join
from slate.services.worklogs import link
from slate.services.worklogs import open_worklog
from slate.services.worklogs import resolve_worklog
from slate.services.write_effects import Effects
from slate.services.write_effects import WriteEffectCode
from slate.services.write_effects import WriteEffects

logger = structlog.get_logger()
router = APIRouter()

WorklogStateFilter = Literal['open', 'expired', 'ended', 'all']


def path_worklog(name: str, session: DbSession) -> models.Worklog:
    return resolve_worklog(session, name)


WorklogFromPath = Annotated[models.Worklog, Depends(path_worklog)]


def written(session: Session, worklog: models.Worklog, now: dt.datetime, effects: WriteEffects) -> schemas.WorklogWritten:
    session.commit()
    session.refresh(worklog)
    return effects.answer(schemas.WorklogWritten, worklog=worklog_views(session, [worklog], now)[0])


def refuse_joining_unless_open(worklog: models.Worklog, now: dt.datetime, effects: WriteEffects) -> None:
    """A session joins only an open worklog. One that ended, or whose claim ran out, is history."""
    if worklog.ended_ts is not None:
        stopped = f'Worklog {worklog.name} ended as {worklog.outcome} at {effects.moment(worklog.ended_ts)}.'
    elif worklog.claim is not None and worklog_state(worklog, now) == 'expired':
        stopped = f"Worklog {worklog.name}'s claim ran out at {effects.moment(worklog.claim.expires_ts)}."
    else:
        return
    raise HTTPException(
        status_code=status.HTTP_409_CONFLICT,
        detail=f'{stopped} Hold #{worklog.record_number}, or start a worklog on it, to work it again.',
    )


@router.get('', response_model=list[schemas.Worklog], status_code=status.HTTP_200_OK)
def read_many(
    session: DbSession,
    record: str | None = Query(None, description='Worklogs on this record, by number'),
    agent_session: str | None = Query(None, alias='session', description='Worklogs this agent session joined'),
    state: Annotated[WorklogStateFilter, Query(description='open is under way; expired lost its claim; ended is history')] = 'open',
    limit: RowLimit = None,
):
    """Worklogs, latest started first. The default reads the open ones: what is under way now."""
    now = dt.datetime.now(dt.UTC)
    query = (
        select(models.Worklog)
        .outerjoin(models.Claim, models.Claim.id == models.Worklog.claim_id)
        .options(*WORKLOG_LOAD_OPTIONS)
        .order_by(models.Worklog.started_ts.desc(), models.Worklog.id.desc())
    )
    if record is not None:
        query = query.where(models.Worklog.record_number == resolve_record(session, record).number)
    if agent_session is not None:
        query = query.where(
            models.Worklog.id.in_(select(models.WorklogSpan.worklog_id).where(models.WorklogSpan.session_id == agent_session))
        )
    if state == 'open':
        query = query.where(models.Worklog.ended_ts.is_(None), or_(models.Worklog.claim_id.is_(None), models.Claim.is_live(now)))
    elif state == 'expired':
        query = query.where(models.Worklog.ended_ts.is_(None), models.Claim.is_expired(now))
    elif state == 'ended':
        query = query.where(models.Worklog.ended_ts.is_not(None))
    return worklog_views(session, list(session.scalars(apply_row_limit(query, limit)).all()), now)


@router.get('/current', response_model=schemas.Worklog | None, status_code=status.HTTP_200_OK)
def read_current(session: DbSession, agent_session: Annotated[str, Query(alias='session', min_length=1)]):
    """The open worklog the agent session is in now, or null when it is in none.

    A commit hook stamps the commit with this worklog's name. A worklog whose claim ran out is not
    open, so it answers null too.
    """
    now = dt.datetime.now(dt.UTC)
    worklog = current_worklog(session, agent_session, now)
    return worklog_views(session, [worklog], now)[0] if worklog is not None else None


@router.get('/{name}', response_model=schemas.Worklog, status_code=status.HTTP_200_OK)
def read_one(worklog: WorklogFromPath, session: DbSession):
    """One worklog by its name, `<record>.<sequence>`, with its spans, links, claim and waits."""
    return worklog_views(session, [worklog], dt.datetime.now(dt.UTC))[0]


@router.post('', response_model=schemas.WorklogWritten, status_code=status.HTTP_201_CREATED)
def start(request: schemas.WorklogStart, session: DbSession, caller: CurrentCaller, effects: Effects):
    """Open a worklog on a record with no claim, for discussing an idea or an initiative.

    `session` joins it, leaving any other worklog it was in, named in `side_effects`. Each worklog
    already open on the record is a warning, since joining it may be what was meant.
    """
    refuse_acting_for_another(caller, request.opened_by, Refusal.SCOPED_CLIENT_WRITES_WORKLOGS_AS_ITSELF)
    record = resolve_record(session, request.record)
    now = dt.datetime.now(dt.UTC)
    unended = session.scalars(
        select(models.Worklog).where(models.Worklog.record_number == record.number, models.Worklog.ended_ts.is_(None))
    ).all()
    for other in (each for each in unended if worklog_state(each, now) == 'open'):
        effects.warn(
            WriteEffectCode.OPEN_WORKLOG,
            f'Worklog {other.name} on #{record.number} is open since {effects.moment(other.started_ts)}, opened by {other.opened_by}. '
            f'Joining it keeps one go at #{record.number} in one worklog.',
            record.number,
        )
    worklog = open_worklog(session, record.number, request.opened_by, now, workflow=request.workflow)
    if request.session is not None:
        join(session, worklog, request.session, request.host, request.directory, now, effects)
    return written(session, worklog, now, effects)


@router.post('/{name}/join', response_model=schemas.WorklogWritten, status_code=status.HTTP_200_OK)
def join_worklog(worklog: WorklogFromPath, request: schemas.WorklogJoin, session: DbSession, effects: Effects):
    """Put the agent session in the worklog from now, ending its span in any other, named in `side_effects`.

    A session is in one worklog at a time, so its spans never overlap. 409 when the worklog ended,
    or its claim ran out.
    """
    now = dt.datetime.now(dt.UTC)
    refuse_joining_unless_open(worklog, now, effects)
    join(session, worklog, request.session, request.host, request.directory, now, effects)
    return written(session, worklog, now, effects)


@router.post('/{name}/finish', response_model=schemas.WorklogWritten, status_code=status.HTTP_200_OK)
def finish_worklog(worklog: WorklogFromPath, request: schemas.WorklogFinish, session: DbSession, caller: CurrentCaller, effects: Effects):
    """End the worklog by hand, as `finished`, with every span still open in it.

    A claim that opened it keeps holding its record, with a warning. A worklog already ended changes
    nothing and warns. A scoped client finishes only a worklog it opened.
    """
    refuse_acting_for_another(caller, request.finished_by, Refusal.SCOPED_CLIENT_WRITES_WORKLOGS_AS_ITSELF)
    if isinstance(caller, ScopedClient) and worklog.opened_by != caller.client_id:
        raise ForbiddenException(f'{Refusal.SCOPED_CLIENT_FINISHES_ITS_OWN}; {worklog.name} was opened by {worklog.opened_by}', logger)
    now = dt.datetime.now(dt.UTC)
    finish(worklog, request.finished_by, now, effects)
    return written(session, worklog, now, effects)


@router.post('/{name}/links', response_model=schemas.WorklogWritten, status_code=status.HTTP_200_OK)
def link_to_worklog(worklog: WorklogFromPath, request: schemas.WorklogLinkAdd, session: DbSession, caller: CurrentCaller, effects: Effects):
    """Record a commit, pull request or landing the worklog produced. slate stores the ref as given and never follows it.

    A ref the worklog already links changes nothing and warns. A worklog that ended still takes a
    link, since a pull request lands after the claim that made it is done.
    """
    refuse_acting_for_another(caller, request.linked_by, Refusal.SCOPED_CLIENT_WRITES_WORKLOGS_AS_ITSELF)
    now = dt.datetime.now(dt.UTC)
    link(session, worklog, request, now, effects)
    return written(session, worklog, now, effects)


@router.patch('/{name}', response_model=schemas.WorklogWritten, status_code=status.HTTP_200_OK)
def update(worklog: WorklogFromPath, request: schemas.WorklogUpdate, session: DbSession, effects: Effects):
    """Set how the work was approached. Only the fields the body names change, and `workflow: null` clears it."""
    if 'workflow' in request.model_fields_set:
        worklog.workflow = request.workflow
    return written(session, worklog, dt.datetime.now(dt.UTC), effects)
