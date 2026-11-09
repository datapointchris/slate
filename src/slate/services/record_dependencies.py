"""Add and remove what an issue or an initiative waits on, the same way whichever route names it.

An issue waits on each issue it depends on. An initiative waits on an initiative or an issue, and
while both are open every unclosed issue in it is blocked. An edge that would leave a record
waiting on itself is refused with the loop it closes. Each function leaves the commit to its route.
"""

from __future__ import annotations

import datetime as dt
from uuid import UUID

from fastapi import HTTPException
from fastapi import status
from sqlalchemy.orm import Session

from slate import models
from slate.services.record_refs import resolve_issue
from slate.services.record_refs import resolve_issue_or_initiative
from slate.services.wait_cycles import read_wait_graph
from slate.services.wait_cycles import refuse_wait_loop

Ref = UUID | int | str


def add_issue_dependency(session: Session, issue: models.Issue, ref: Ref) -> None:
    depends_on = resolve_issue(session, ref)
    if depends_on.id == issue.id:
        raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail='An issue cannot depend on itself')
    if session.get(models.IssueDependency, (issue.id, depends_on.id)) is not None:
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=f'#{issue.number} already depends on #{depends_on.number}')
    graph = read_wait_graph(session)
    graph.add_dependency(issue.number, depends_on.number)
    refuse_wait_loop(graph, issue.number)
    session.add(models.IssueDependency(issue_id=issue.id, depends_on_id=depends_on.id))
    issue.updated_ts = dt.datetime.now(dt.UTC)


def remove_issue_dependency(session: Session, issue: models.Issue, ref: Ref) -> None:
    depends_on = resolve_issue(session, ref)
    edge = session.get(models.IssueDependency, (issue.id, depends_on.id))
    if edge is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail=f'#{issue.number} does not depend on #{depends_on.number}')
    session.delete(edge)
    issue.updated_ts = dt.datetime.now(dt.UTC)


def add_initiative_wait(session: Session, initiative: models.Initiative, ref: Ref) -> None:
    depends_on = resolve_issue_or_initiative(session, ref)
    if depends_on.number == initiative.number:
        raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail='An initiative cannot wait on itself')
    if session.get(models.InitiativeDependency, (initiative.id, depends_on.number)) is not None:
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=f'#{initiative.number} already waits on #{depends_on.number}')
    graph = read_wait_graph(session)
    graph.add_wait(initiative.number, depends_on.number)
    refuse_wait_loop(graph, initiative.number, depends_on.number)
    session.add(models.InitiativeDependency(initiative_id=initiative.id, depends_on_number=depends_on.number))


def remove_initiative_wait(session: Session, initiative: models.Initiative, ref: Ref) -> None:
    depends_on = resolve_issue_or_initiative(session, ref)
    edge = session.get(models.InitiativeDependency, (initiative.id, depends_on.number))
    if edge is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail=f'#{initiative.number} does not wait on #{depends_on.number}')
    session.delete(edge)
