"""The totals of the work over any record, worklog, repo or window.

No one noun owns this read. A window's total counts tokens no worklog held, and a repo is no
record, so it sits at its own path rather than under `/records` or `/worklogs`.
"""

import datetime as dt
from typing import Annotated

from fastapi import APIRouter
from fastapi import Query
from fastapi import status

from slate import schemas
from slate.api.callers import DbSession
from slate.api.time_window import TimeWindowQuery
from slate.services.record_refs import resolve_record
from slate.services.work_totals import work_totals
from slate.services.worklogs import resolve_worklog
from slate.tracing.named_work import name_record
from slate.tracing.named_work import name_worklog

router = APIRouter()


@router.get('', response_model=schemas.WorkTotals, status_code=status.HTTP_200_OK)
def read_totals(
    session: DbSession,
    window: TimeWindowQuery,
    record: Annotated[
        str | None,
        Query(description='A record number, as N or #N: its worklogs and those of everything under it'),
    ] = None,
    worklog: Annotated[str | None, Query(description='A worklog name, such as 1991.2')] = None,
    repo: Annotated[str | None, Query(description="A repo name: its issues' worklogs. An empty string reaches issues on no repo")] = None,
) -> schemas.WorkTotals:
    """Worklogs, sessions, commits, pull requests, landings, messages and tokens by kind over one scope.

    Each of `record`, `worklog` and `repo` narrows the worklogs the totals reach, and named together
    they narrow together. Naming none reaches every worklog, and every message no worklog held. The
    window narrows each figure to what happened inside it.
    """
    named_worklog = resolve_worklog(session, worklog) if worklog is not None else None
    if named_worklog is not None:
        name_record(named_worklog.record_number)
        name_worklog(named_worklog)
    named_record = resolve_record(session, record) if record is not None else None
    if named_record is not None:
        name_record(named_record.number)
    return work_totals(session, dt.datetime.now(dt.UTC), window, record=named_record, worklog=named_worklog, repo=repo)
