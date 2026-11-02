import datetime as dt
from collections import deque
from typing import Annotated
from uuid import UUID

import structlog
from fastapi import APIRouter
from fastapi import Depends
from fastapi import HTTPException
from fastapi import Query
from fastapi import Response
from fastapi import status
from sqlalchemy import and_
from sqlalchemy import delete as sql_delete
from sqlalchemy import or_
from sqlalchemy import select
from sqlalchemy import update as sql_update
from sqlalchemy.orm import Session
from sqlalchemy.sql import Select

from slate import models
from slate import schemas
from slate.api.callers import CurrentCaller
from slate.api.callers import DbSession
from slate.api.client_scopes import ISSUES_CLOSE
from slate.api.client_scopes import ISSUES_PLAN
from slate.api.exceptions import ForbiddenException
from slate.api.exceptions import NotFoundException
from slate.api.exceptions import Refusal
from slate.api.oidc_auth import ScopedClient
from slate.api.request_zone import RequestZone
from slate.api.time_window import TimeWindowQuery
from slate.models.initiative import INITIATIVE_STATUSES
from slate.models.initiative import TERMINAL_INITIATIVE_STATUSES
from slate.models.issue import CLOSED_ISSUE_STATUSES
from slate.models.issue import ISSUE_STATUSES
from slate.models.issue import ISSUE_TYPES
from slate.models.record import PRIORITIES
from slate.services.issue_rank import move_issue
from slate.services.issue_rank import rank_at_end
from slate.services.issue_readiness import IssueReadiness
from slate.services.issue_readiness import measure_readiness
from slate.services.issue_views import ISSUE_LOAD_OPTIONS
from slate.services.issue_views import calendar_now
from slate.services.issue_views import issue_detail
from slate.services.issue_views import issue_views
from slate.services.issue_views import label_views
from slate.services.issue_views import list_order
from slate.services.record_refs import resolve_initiative
from slate.services.record_refs import resolve_issue
from slate.services.record_refs import resolve_label
from slate.services.record_refs import resolve_labels
from slate.services.row_limit import RowLimit
from slate.services.time_window import apply_time_window
from slate.services.write_effects import Effects
from slate.services.write_effects import WriteEffectCode
from slate.services.write_effects import WriteEffects

logger = structlog.get_logger()
router = APIRouter()

# Not a status, so it is not in the lookup table: `all` is the absence of the
# filter, and storing it would make it assignable to an issue.
ALL_STATUSES = 'all'

# The statuses a new issue may start in. Anything later is reached by working it.
CREATE_STATUSES = ('triage', 'open')


def path_issue(id: str, session: DbSession) -> models.Issue:
    """Resolve the `{id}` segment, which is a UUID or an issue number."""
    return resolve_issue(session, id)


IssueFromPath = Annotated[models.Issue, Depends(path_issue)]


def validate_lookup(session: Session, model, value: str, noun: str, extra: tuple[str, ...] = ()) -> None:
    """Reject an unknown vocabulary value by name rather than letting the foreign key raise."""
    if value in extra or session.get(model, value) is not None:
        return
    known = [*session.scalars(select(model.name).order_by(model.name)).all(), *extra]
    raise HTTPException(
        status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
        detail=f'Unknown {noun} {value!r}. Known: {", ".join(known)}',
    )


def apply_repo_filter(query: Select, repo: str | None) -> Select:
    """Narrow to one repo; an explicit empty string asks for the issues on no repo."""
    if repo is None:
        return query
    if repo == '':
        return query.where(models.Issue.repo.is_(None))
    return query.where(models.Issue.repo == repo)


def apply_label_filter(query: Select, label: str | None, session: Session) -> Select:
    if label is None:
        return query
    label_row = resolve_label(session, label)
    return query.where(
        models.Issue.id.in_(select(models.IssueLabelAssignment.issue_id).where(models.IssueLabelAssignment.label_id == label_row.id))
    )


def waited_on_by(session: Session, issue_id: UUID) -> list[UUID]:
    """What `issue_id` waits on: the issues it depends on, and its children."""
    dependencies = select(models.IssueDependency.depends_on_id).where(models.IssueDependency.issue_id == issue_id)
    children = select(models.Issue.id).where(models.Issue.parent_id == issue_id)
    return [*session.scalars(dependencies).all(), *session.scalars(children).all()]


def ensure_no_wait_cycle(session: Session, waiter: models.Issue, waited_on: models.Issue) -> None:
    """Refuse a new edge that would leave an issue waiting on itself.

    An issue waits on what it depends on and, as a parent, on its open children.
    A cycle through either kind of edge is a deadlock: nothing in it can become
    ready. The new edge closes one exactly when `waited_on` already reaches
    `waiter`, and the refusal names the path so the caller sees which edge to cut.
    """
    came_from: dict[UUID, UUID | None] = {waited_on.id: None}
    queue = deque([waited_on.id])
    while queue:
        current = queue.popleft()
        if current == waiter.id:
            path: list[UUID] = []
            step: UUID | None = current
            while step is not None:
                path.append(step)
                step = came_from[step]
            numbers = dict(session.execute(select(models.Issue.id, models.Issue.number).where(models.Issue.id.in_(path))).all())
            chain = ' → '.join(f'#{numbers[i]}' for i in [waiter.id, *reversed(path)])
            raise HTTPException(
                status_code=status.HTTP_409_CONFLICT,
                detail=f'That would leave #{waiter.number} waiting on itself: {chain}',
            )
        for following in waited_on_by(session, current):
            if following not in came_from:
                came_from[following] = current
                queue.append(following)


def ensure_initiative_open(initiative: models.Initiative) -> None:
    if initiative.status in TERMINAL_INITIATIVE_STATUSES:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'Initiative {initiative.name!r} is {initiative.status}. Reopen it before filing work into it.',
        )


def set_labels(session: Session, issue: models.Issue, slugs: list[str]) -> None:
    """Make the issue's labels exactly `slugs`, keeping the assignment rows that stay."""
    wanted = {label.id for label in resolve_labels(session, slugs)}
    kept = [assignment for assignment in issue.label_assignments if assignment.label_id in wanted]
    held = {assignment.label_id for assignment in kept}
    issue.label_assignments = kept + [models.IssueLabelAssignment(label_id=label_id) for label_id in sorted(wanted - held)]


def ensure_parent_open(parent: models.Issue) -> None:
    """A closed issue takes no unclosed children: a completed one would be finished with work still open."""
    if parent.is_closed:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'#{parent.number} is {parent.status}. Reopen it before filing work under it.',
        )


def reopen_completed_ancestors(issue: models.Issue, now: dt.datetime, effects: WriteEffects) -> None:
    """Return each completed ancestor to open, because a completed issue never holds open work."""
    parent = issue.parent
    while parent is not None and parent.status == 'completed':
        parent.status = 'open'
        parent.closed_ts = None
        parent.closed_by = None
        parent.updated_ts = now
        effects.did(
            WriteEffectCode.ANCESTOR_REOPENED,
            f'Reopened #{parent.number}, because its descendant #{issue.number} reopened and a completed issue holds no open work.',
            parent.number,
        )
        parent = parent.parent


def clear_claim(issue: models.Issue) -> None:
    issue.claimed_by = None
    issue.claim_expires_ts = None


def numbered(issues: list[models.Issue]) -> str:
    return ', '.join(f'#{issue.number}' for issue in sorted(issues, key=lambda issue: issue.number))


def unprocessable(detail: str) -> HTTPException:
    return HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail=detail)


def resulting_duplicate(session: Session, issue: models.Issue, update_data: dict, target: str) -> UUID | None:
    """The duplicate link the issue ends up with: what was sent, else what it had if it stays canceled."""
    if 'duplicate_of' not in update_data:
        return issue.duplicate_of_id if target == 'canceled' else None
    ref = update_data.pop('duplicate_of')
    if ref is None:
        return None
    if target != 'canceled':
        raise unprocessable('duplicate_of is set only on a canceled issue. Send status canceled with it.')
    duplicate = resolve_issue(session, ref)
    if duplicate.id == issue.id:
        raise unprocessable('An issue cannot duplicate itself')
    return duplicate.id


def resulting_reason(issue: models.Issue, update_data: dict, target: str) -> str | None:
    """The cancel reason the issue ends up with. Only a canceled issue keeps one."""
    if 'status_reason' not in update_data:
        return issue.status_reason if target == 'canceled' else None
    reason = update_data.pop('status_reason')
    if reason is not None and target != 'canceled':
        raise unprocessable('status_reason records why an issue was canceled. Send status canceled with it.')
    return reason


def apply_status_transition(
    session: Session, issue: models.Issue, update_data: dict, now: dt.datetime, effects: WriteEffects, *, signed_in: str
) -> None:
    """Validate a status change and apply everything it implies, in one place.

    Closing stamps `closed_ts` and `closed_by` and drops the claim. `closed_by`
    is the actor the caller named, else `signed_in`, because a token is the
    user's whichever session or worker holds it. A move between two closed
    statuses keeps both stamps. Reopening clears them, the reason and any
    duplicate link, and reopens each completed ancestor, naming each in
    `effects`. Leaving `in_progress` for anything but a close releases the
    claim. The CHECK constraints refuse each of these states too; this exists
    so a caller gets a 4xx naming the problem.
    """
    new_status = update_data.pop('status', None)
    if new_status is not None:
        validate_lookup(session, models.IssueStatus, new_status, 'issue status')
    target = new_status or issue.status

    closer = update_data.pop('closed_by', None)
    closing = target in CLOSED_ISSUE_STATUSES and not issue.is_closed
    if closer is not None and not closing:
        raise unprocessable('closed_by records who closes an issue. Send it with the status that closes an open one.')

    duplicate_id = resulting_duplicate(session, issue, update_data, target)
    reason = resulting_reason(issue, update_data, target)
    if target == 'canceled' and not reason and duplicate_id is None:
        raise unprocessable('Canceling an issue requires a reason, or the issue it duplicates.')

    if target == 'completed' and issue.status != 'completed':
        open_children = [child for child in issue.children if not child.is_closed]
        if open_children:
            raise HTTPException(
                status_code=status.HTTP_409_CONFLICT,
                detail=f'#{issue.number} still has open children: {numbered(open_children)}. Close them first.',
            )

    if target in CLOSED_ISSUE_STATUSES:
        if closing:
            issue.closed_ts = now
            issue.closed_by = closer or signed_in
        clear_claim(issue)
    else:
        if issue.is_closed:
            reopen_completed_ancestors(issue, now, effects)
        issue.closed_ts = None
        issue.closed_by = None
        if target != 'in_progress':
            clear_claim(issue)
    issue.status = target
    issue.status_reason = reason
    issue.duplicate_of_id = duplicate_id


def load_issue(session: Session, issue_id: UUID) -> models.Issue:
    issue = session.scalars(select(models.Issue).options(*ISSUE_LOAD_OPTIONS).where(models.Issue.id == issue_id)).one()
    return issue


def fresh_issue_detail(session: Session, issue: models.Issue, zone: str) -> schemas.IssueDetail:
    session.expire_all()
    return issue_detail(session, load_issue(session, issue.id), zone)


def single_view(session: Session, issue: models.Issue, zone: str) -> schemas.Issue:
    session.expire_all()
    return issue_views(session, [load_issue(session, issue.id)], zone)[0]


# `/ready` and `/vocabulary` are declared ahead of `/{id}`, which would
# otherwise take either segment as an issue reference.

AUTOFIXABLE_FILTER = "True for issues whose own, initiative's and parents' autofix are all on, false for the rest"


@router.get('', response_model=list[schemas.Issue], status_code=status.HTTP_200_OK)
def read_many(
    session: DbSession,
    zone: RequestZone,
    window: TimeWindowQuery,
    issue_status: str | None = Query(
        None,
        alias='status',
        description="An issue status, or 'all'. Omitted, every issue not yet completed or canceled.",
    ),
    repo: str | None = Query(None, description='A repo name, or an empty string for issues on no repo'),
    type: str | None = Query(None, description='An issue type'),
    label: str | None = Query(None, description='A label slug'),
    initiative: str | None = Query(None, description='An initiative, by any reference GET /issues/initiatives/{id} accepts'),
    parent: str | None = Query(None, description='A parent issue number or UUID: its children'),
    priority: int | None = Query(None, ge=0, le=4, description="The issue's own priority"),
    claimed_by: str | None = Query(None, description='Issues held by this claimant'),
    closed_by: str | None = Query(None, description="Issues whose closer starts with this, such as 'claude-code/'"),
    search: str | None = Query(None, description='Words that must each appear in the title or description, in any order'),
    blocked: bool | None = Query(None, description='True for issues waiting on an unclosed dependency, false for the rest'),
    autofixable: bool | None = Query(None, description=AUTOFIXABLE_FILTER),
    limit: RowLimit = None,
):
    """List issues, open work first by effective priority then rank, closed work after it.

    Completed and canceled issues accumulate without bound, so the default leaves
    them out. The window narrows on `closed_ts`, the one date that records an
    issue finishing rather than being edited. Blocked is derived on every read,
    so it narrows after the query and before the limit.

    `closed_by` matches a prefix because an actor's prefix is its kind, so
    `claude-code/` reads every attended session's closes.
    """
    query = select(models.Issue).options(*ISSUE_LOAD_OPTIONS)
    if issue_status is None:
        query = query.where(models.Issue.status.not_in(CLOSED_ISSUE_STATUSES))
    elif issue_status != ALL_STATUSES:
        validate_lookup(session, models.IssueStatus, issue_status, 'issue status', extra=(ALL_STATUSES,))
        query = query.where(models.Issue.status == issue_status)
    if type is not None:
        validate_lookup(session, models.IssueType, type, 'issue type')
        query = query.where(models.Issue.type == type)
    if initiative is not None:
        query = query.where(models.Issue.initiative_id == resolve_initiative(session, initiative).id)
    if parent is not None:
        query = query.where(models.Issue.parent_id == resolve_issue(session, parent).id)
    if priority is not None:
        query = query.where(models.Issue.priority == priority)
    if claimed_by is not None:
        query = query.where(models.Issue.claimed_by == claimed_by)
    if closed_by:
        query = query.where(models.Issue.closed_by.startswith(closed_by, autoescape=True))
    for word in (search or '').split():
        query = query.where(
            or_(models.Issue.title.icontains(word, autoescape=True), models.Issue.description.icontains(word, autoescape=True))
        )
    query = apply_repo_filter(query, repo)
    query = apply_label_filter(query, label, session)
    query = apply_time_window(query, models.Issue.closed_ts, window, zone=None)

    today, now = calendar_now(zone)
    readiness = measure_readiness(session, today, now)
    issues = sorted(session.scalars(query).all(), key=list_order(readiness))
    if blocked is not None:
        issues = [issue for issue in issues if (issue.id in readiness.blocked) == blocked]
    issues = autofixable_filtered(issues, readiness, autofixable)
    if limit is not None:
        issues = issues[:limit]
    return issue_views(session, issues, zone, readiness)


def autofixable_filtered(issues: list[models.Issue], readiness: IssueReadiness, autofixable: bool | None) -> list[models.Issue]:
    if autofixable is None:
        return issues
    return [issue for issue in issues if (issue.id in readiness.autofixable) == autofixable]


def ready_issues(
    session: Session,
    zone: str,
    *,
    repo: str | None,
    type: str | None,
    label: str | None,
    initiative: str | UUID | int | None,
    autofixable: bool | None,
) -> tuple[list[models.Issue], IssueReadiness]:
    """The ready queue in the order it is taken, and the readiness it was cut from.

    Decisions wait on a person, so the queue leaves them out unless `type` asks
    for them by name.
    """
    today, now = calendar_now(zone)
    readiness = measure_readiness(session, today, now)
    query = select(models.Issue).options(*ISSUE_LOAD_OPTIONS).where(models.Issue.id.in_(readiness.ready))
    if type is not None:
        validate_lookup(session, models.IssueType, type, 'issue type')
        query = query.where(models.Issue.type == type)
    else:
        query = query.where(models.Issue.type != 'decision')
    if initiative is not None:
        query = query.where(models.Issue.initiative_id == resolve_initiative(session, initiative).id)
    query = apply_repo_filter(query, repo)
    query = apply_label_filter(query, label, session)
    issues = sorted(session.scalars(query).all(), key=readiness.sort_key)
    return autofixable_filtered(issues, readiness, autofixable), readiness


@router.get('/ready', response_model=list[schemas.Issue], status_code=status.HTTP_200_OK)
def read_ready(
    session: DbSession,
    zone: RequestZone,
    repo: str | None = Query(None, description='A repo name, or an empty string for issues on no repo'),
    type: str | None = Query(None, description='An issue type. Decisions are left out unless asked for here.'),
    label: str | None = Query(None, description='A label slug'),
    initiative: str | None = Query(None, description='An initiative, by any reference GET /issues/initiatives/{id} accepts'),
    autofixable: bool | None = Query(None, description=AUTOFIXABLE_FILTER),
    limit: RowLimit = None,
):
    """The issues an agent could take now, in the order to take them; the first row is next."""
    issues, readiness = ready_issues(session, zone, repo=repo, type=type, label=label, initiative=initiative, autofixable=autofixable)
    if limit is not None:
        issues = issues[:limit]
    return issue_views(session, issues, zone, readiness)


def claim_values(claimant: str, minutes: int, now: dt.datetime) -> dict:
    return {
        'status': 'in_progress',
        'claimed_by': claimant,
        'claim_expires_ts': now + dt.timedelta(minutes=minutes),
        'updated_ts': now,
    }


def claimable(now: dt.datetime, claimant: str | None = None):
    """The compare half of the claim's compare-and-set: no live claim by anyone else."""
    free = or_(
        models.Issue.status == 'open',
        and_(models.Issue.status == 'in_progress', models.Issue.claim_expires_ts.is_not(None), models.Issue.claim_expires_ts <= now),
    )
    if claimant is None:
        return free
    return or_(free, and_(models.Issue.status == 'in_progress', models.Issue.claimed_by == claimant))


@router.post('/ready/claim', response_model=schemas.IssueClaimResult, status_code=status.HTTP_200_OK)
def claim_next(request: schemas.IssueReadyClaimRequest, session: DbSession, zone: RequestZone):
    """Take the head of the ready queue in one step.

    Each candidate is taken with a compare-and-set, so two agents asking at once
    never hold the same issue: the loser's update matches no row and it moves on
    to the next candidate.
    """
    issues, _ = ready_issues(
        session,
        zone,
        repo=request.repo,
        type=request.type,
        label=request.label,
        initiative=request.initiative,
        autofixable=request.autofixable,
    )
    now = dt.datetime.now(dt.UTC)
    for candidate in issues:
        taken = session.execute(
            sql_update(models.Issue)
            .where(models.Issue.id == candidate.id, claimable(now))
            .values(**claim_values(request.claimant, request.minutes, now))
            .returning(models.Issue.id),
            execution_options={'synchronize_session': False},
        ).scalar_one_or_none()
        if taken is not None:
            session.commit()
            logger.info('issue_claimed', number=candidate.number, claimant=request.claimant)
            return schemas.IssueClaimResult(issue=single_view(session, candidate, zone))
    return schemas.IssueClaimResult(issue=None)


@router.get('/vocabulary', response_model=schemas.IssueVocabulary, status_code=status.HTTP_200_OK)
def read_vocabulary(session: DbSession):
    """Every value the closed issue fields accept, with the open issue count per label.

    Statuses and types come back in their lifecycle order rather than the
    table's, so a client can render them as given.
    """
    statuses = set(session.scalars(select(models.IssueStatus.name)).all())
    types = set(session.scalars(select(models.IssueType.name)).all())
    initiative_statuses = set(session.scalars(select(models.InitiativeStatus.name)).all())
    return schemas.IssueVocabulary(
        statuses=in_declared_order(statuses, ISSUE_STATUSES),
        types=in_declared_order(types, ISSUE_TYPES),
        priorities=[schemas.IssuePriorityName(value=value, name=name) for value, name in PRIORITIES.items()],
        initiative_statuses=in_declared_order(initiative_statuses, INITIATIVE_STATUSES),
        labels=label_views(session),
    )


def in_declared_order(values: set[str], declared: list[str]) -> list[str]:
    """The declared values first in their order, then any the table holds beyond them."""
    return [v for v in declared if v in values] + sorted(values - set(declared))


def refuse_all_but_a_triage_filing(issue: schemas.IssueCreate) -> None:
    """A scoped client's filing waits for a person, and names no parent, which would wait on it."""
    if issue.parent is not None:
        raise ForbiddenException(f'{Refusal.SCOPED_CLIENT_FILES_ONLY_INTO_TRIAGE}; this filing names a parent', logger)
    if issue.status != 'triage':
        raise ForbiddenException(f'{Refusal.SCOPED_CLIENT_FILES_ONLY_INTO_TRIAGE}; this filing starts as {issue.status}', logger)


@router.post('', response_model=schemas.IssueWritten, status_code=status.HTTP_201_CREATED)
def create(issue: schemas.IssueCreate, session: DbSession, zone: RequestZone, caller: CurrentCaller, effects: Effects):
    if isinstance(caller, ScopedClient):
        refuse_all_but_a_triage_filing(issue)
    if issue.status not in CREATE_STATUSES:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail=f'A new issue starts as {" or ".join(CREATE_STATUSES)}, not {issue.status!r}',
        )
    validate_lookup(session, models.IssueType, issue.type, 'issue type')

    db_issue = models.Issue(
        title=issue.title,
        description=issue.description,
        acceptance=issue.acceptance,
        repo=issue.repo,
        type=issue.type,
        status=issue.status,
        priority=issue.priority,
        deferred_until_date=issue.deferred_until_date,
        autofix=issue.autofix,
        rank=rank_at_end(session),
    )
    if issue.initiative is not None:
        initiative = resolve_initiative(session, issue.initiative)
        ensure_initiative_open(initiative)
        db_issue.initiative_id = initiative.id
    if issue.parent is not None:
        parent = resolve_issue(session, issue.parent)
        ensure_parent_open(parent)
        db_issue.parent_id = parent.id
    if issue.discovered_from is not None:
        db_issue.discovered_from_id = resolve_issue(session, issue.discovered_from).id
    depends_on = [resolve_issue(session, ref) for ref in dict.fromkeys(issue.depends_on)]
    session.add(db_issue)
    set_labels(session, db_issue, issue.labels)
    session.flush()
    # Only through the parent can a brand-new issue close a cycle: depending on
    # its own parent, or on anything that waits on that parent.
    for blocker in depends_on:
        ensure_no_wait_cycle(session, db_issue, blocker)
        session.add(models.IssueDependency(issue_id=db_issue.id, depends_on_id=blocker.id))
        session.flush()
    session.commit()
    logger.info('issue_created', number=db_issue.number, repo=db_issue.repo)
    return effects.answer(schemas.IssueWritten, issue=fresh_issue_detail(session, db_issue, zone))


@router.get('/{id}', response_model=schemas.IssueDetail, status_code=status.HTTP_200_OK)
def read_one(issue: IssueFromPath, session: DbSession, zone: RequestZone):
    return fresh_issue_detail(session, issue, zone)


# What a scoped client's update may carry. slate.issues.close reaches this route to complete an issue
# whose fix merged, and slate.issues.plan to re-tier an issue or move it between initiatives. A body
# naming a close field is a close, and any other is an edit.
SCOPED_CLOSE_FIELDS = frozenset({'status', 'closed_by'})
SCOPED_PLAN_FIELDS = frozenset({'priority', 'initiative'})


def refuse_all_but_a_close(issue: models.Issue, update_data: dict) -> None:
    if extra := sorted(set(update_data) - SCOPED_CLOSE_FIELDS):
        raise ForbiddenException(f'{Refusal.SCOPED_CLIENT_ONLY_CLOSES}; this update also sets {", ".join(extra)}', logger)
    if update_data.get('status') not in CLOSED_ISSUE_STATUSES:
        raise ForbiddenException(f'{Refusal.SCOPED_CLIENT_ONLY_CLOSES}; this update sets no closing status', logger)
    if issue.is_closed:
        raise ForbiddenException(f'{Refusal.SCOPED_CLIENT_ONLY_CLOSES}; #{issue.number} is already {issue.status}', logger)


def refuse_all_but_a_plan(update_data: dict) -> None:
    if extra := sorted(set(update_data) - SCOPED_PLAN_FIELDS):
        raise ForbiddenException(
            f'{Refusal.SCOPED_CLIENT_ONLY_SETS_PRIORITY_AND_INITIATIVE}; this update also sets {", ".join(extra)}', logger
        )


def refuse_a_scoped_update(issue: models.Issue, update_data: dict, scopes: frozenset[str]) -> None:
    """Narrow the body to the scope it is aimed at. The route gate admits a client holding either."""
    closing = not SCOPED_CLOSE_FIELDS.isdisjoint(update_data)
    if ISSUES_PLAN in scopes and not (closing and ISSUES_CLOSE in scopes):
        refuse_all_but_a_plan(update_data)
    else:
        refuse_all_but_a_close(issue, update_data)


@router.patch('/{id}', response_model=schemas.IssueWritten, status_code=status.HTTP_200_OK)
def update(
    issue: IssueFromPath, update: schemas.IssueUpdate, session: DbSession, zone: RequestZone, caller: CurrentCaller, effects: Effects
):
    update_data = update.model_dump(exclude_unset=True)
    logger.debug('issue_update', number=issue.number, fields=sorted(update_data))
    now = dt.datetime.now(dt.UTC)
    if isinstance(caller, ScopedClient):
        refuse_a_scoped_update(issue, update_data, caller.scopes)
        signed_in = caller.client_id
    else:
        signed_in = caller.subject

    if (issue_type := update_data.get('type')) is not None:
        validate_lookup(session, models.IssueType, issue_type, 'issue type')
    if 'initiative' in update_data:
        ref = update_data.pop('initiative')
        if ref is None:
            issue.initiative_id = None
        else:
            initiative = resolve_initiative(session, ref)
            if initiative.id != issue.initiative_id:
                ensure_initiative_open(initiative)
            issue.initiative_id = initiative.id
    if 'parent' in update_data:
        ref = update_data.pop('parent')
        # The relationship, not the column, so a reopen in this same update walks
        # up from the new parent rather than the one still loaded.
        if ref is None:
            issue.parent = None
        else:
            parent = resolve_issue(session, ref)
            if parent.id != issue.parent_id:
                if update_data.get('status', issue.status) not in CLOSED_ISSUE_STATUSES:
                    ensure_parent_open(parent)
                ensure_no_wait_cycle(session, parent, issue)
            issue.parent = parent
    if 'discovered_from' in update_data:
        ref = update_data.pop('discovered_from')
        issue.discovered_from_id = None if ref is None else resolve_issue(session, ref).id
    if 'labels' in update_data:
        set_labels(session, issue, update_data.pop('labels'))

    apply_status_transition(session, issue, update_data, now, effects, signed_in=signed_in)

    for attr, value in update_data.items():
        setattr(issue, attr, value)
    issue.updated_ts = now
    session.commit()
    return effects.answer(schemas.IssueWritten, issue=fresh_issue_detail(session, issue, zone))


@router.delete('/{id}', status_code=status.HTTP_204_NO_CONTENT)
def delete(issue: IssueFromPath, session: DbSession):
    """Delete the issue's record, which takes the issue, its comments and its edges with it."""
    session.execute(sql_delete(models.Record).where(models.Record.number == issue.number))
    session.commit()
    return Response(status_code=status.HTTP_204_NO_CONTENT)


def claim_conflict(issue: models.Issue) -> str:
    """Name who holds the issue, so the refused agent can tell whether to wait or move on."""
    if issue.claimed_by is None or issue.claim_expires_ts is None:
        # Moved to in_progress by hand, which is a person working it.
        return f'#{issue.number} is in progress without a claim. Release it to open before claiming it.'
    until = issue.claim_expires_ts.astimezone(dt.UTC)
    return f'#{issue.number} is claimed by {issue.claimed_by} until {until:%Y-%m-%d %H:%M} UTC'


def unready_reasons(issue: models.Issue, readiness: IssueReadiness) -> list[str]:
    """Why the ready queue leaves the issue out, a sentence for each reason that holds.

    An issue meeting every condition here and still left out is held: in
    progress under a live claim, or by a person. That reason is `claim_conflict`.
    """
    if issue.is_closed:
        return [f'#{issue.number} is {issue.status}.']
    if issue.status == 'triage':
        return [f'#{issue.number} is in triage. Accept it as open before claiming it.']
    reasons = []
    if issue.id in readiness.blocked:
        blockers = [edge.depends_on for edge in issue.dependencies if not edge.depends_on.is_closed]
        reasons.append(f'#{issue.number} is blocked by {numbered(blockers)}.')
    if readiness.open_child_count.get(issue.id):
        children = [child for child in issue.children if not child.is_closed]
        reasons.append(f'#{issue.number} waits on its open children: {numbered(children)}.')
    if issue.id in readiness.deferred:
        reasons.append(f'#{issue.number} is deferred until {issue.deferred_until_date}.')
    if issue.id in readiness.shelved and issue.initiative is not None:
        reasons.append(
            f'#{issue.number} is in the someday initiative {issue.initiative.name!r}, and nothing outside someday depends on it. '
            'Reopen the initiative, or make active work depend on it.'
        )
    return reasons


@router.post('/{id}/claim', response_model=schemas.Issue, status_code=status.HTTP_200_OK)
def claim(issue: IssueFromPath, request: schemas.IssueClaimRequest, session: DbSession, zone: RequestZone, caller: CurrentCaller):
    """Take this issue, or extend the claim already held under the same name.

    A named issue is taken only when it is ready, a decision included, so an
    agent never holds work it could not finish. Only extending a claim skips
    the readiness check. A scoped client claims under its own client id, which
    is what lets its release be limited to its own claims.
    """
    if isinstance(caller, ScopedClient) and request.claimant != caller.client_id:
        raise ForbiddenException(f'{Refusal.SCOPED_CLIENT_CLAIMS_AS_ITSELF}, {caller.client_id}, not {request.claimant}', logger)
    extending = issue.status == 'in_progress' and issue.claimed_by == request.claimant
    today, now = calendar_now(zone)
    if not extending:
        readiness = measure_readiness(session, today, now)
        if issue.id not in readiness.ready:
            detail = ' '.join(unready_reasons(issue, readiness)) or claim_conflict(issue)
            raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=detail)

    taken = session.execute(
        sql_update(models.Issue)
        .where(models.Issue.id == issue.id, claimable(now, request.claimant))
        .values(**claim_values(request.claimant, request.minutes, now))
        .returning(models.Issue.id),
        execution_options={'synchronize_session': False},
    ).scalar_one_or_none()
    if taken is None:
        session.rollback()
        session.refresh(issue)
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=claim_conflict(issue))
    session.commit()
    logger.info('issue_claimed', number=issue.number, claimant=request.claimant)
    return single_view(session, issue, zone)


@router.delete('/{id}/claim', response_model=schemas.Issue, status_code=status.HTTP_200_OK)
def release(issue: IssueFromPath, session: DbSession, zone: RequestZone, caller: CurrentCaller):
    """Give the issue back to the ready queue. Releasing an issue not in progress changes nothing.

    A claim sits only on an in-progress issue, so this one test covers both a
    claim and work a person moved to in progress by hand. A scoped client lets
    go only of a claim its own client id holds.
    """
    if isinstance(caller, ScopedClient) and issue.status == 'in_progress' and issue.claimed_by != caller.client_id:
        held = f'claimed by {issue.claimed_by}' if issue.claimed_by else 'in progress by hand'
        raise ForbiddenException(f'{Refusal.SCOPED_CLIENT_RELEASES_ITS_OWN}; #{issue.number} is {held}', logger)
    if issue.status == 'in_progress':
        clear_claim(issue)
        issue.status = 'open'
        issue.updated_ts = dt.datetime.now(dt.UTC)
        session.commit()
    return single_view(session, issue, zone)


def priority_phrase(priority: int) -> str:
    return 'no priority' if priority == 0 else f'{PRIORITIES[priority]} priority'


def ensure_same_priority(issue: models.Issue, neighbor: models.Issue, readiness: IssueReadiness) -> None:
    mine, theirs = readiness.effective_priority[issue.id], readiness.effective_priority[neighbor.id]
    if mine != theirs:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=(
                f'#{issue.number} sorts at {priority_phrase(mine)} and #{neighbor.number} at {priority_phrase(theirs)}. '
                f'Rank orders issues only within one priority. '
                f'Changing the priority of #{issue.number} is what moves it past #{neighbor.number}.'
            ),
        )


@router.post('/{id}/rank', response_model=schemas.IssueWritten, status_code=status.HTTP_200_OK)
def rank(issue: IssueFromPath, move: schemas.IssueRankMove, session: DbSession, zone: RequestZone, effects: Effects):
    """Place this issue directly before or after another of the same effective priority.

    Rank orders issues within one priority. Beside an issue of another priority,
    the move would report success and land wherever the other priorities' ranks
    happen to fall, so it is refused.
    """
    before = resolve_issue(session, move.before) if move.before is not None else None
    after = resolve_issue(session, move.after) if move.after is not None else None
    neighbors = [neighbor for neighbor in (before, after) if neighbor is not None]
    if any(neighbor.id == issue.id for neighbor in neighbors):
        raise unprocessable('An issue cannot be ranked beside itself')
    readiness = measure_readiness(session, *calendar_now(zone))
    for neighbor in neighbors:
        ensure_same_priority(issue, neighbor, readiness)
    move_issue(session, issue, before=before, after=after)
    session.commit()
    return effects.answer(schemas.IssueWritten, issue=fresh_issue_detail(session, issue, zone))


@router.post('/{id}/dependencies', response_model=schemas.IssueWritten, status_code=status.HTTP_201_CREATED)
def add_dependency(
    issue: IssueFromPath, dependency: schemas.IssueDependencyCreate, session: DbSession, zone: RequestZone, effects: Effects
):
    depends_on = resolve_issue(session, dependency.depends_on)
    if depends_on.id == issue.id:
        raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail='An issue cannot depend on itself')
    if session.get(models.IssueDependency, (issue.id, depends_on.id)) is not None:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'#{issue.number} already depends on #{depends_on.number}',
        )
    ensure_no_wait_cycle(session, issue, depends_on)
    session.add(models.IssueDependency(issue_id=issue.id, depends_on_id=depends_on.id))
    issue.updated_ts = dt.datetime.now(dt.UTC)
    session.commit()
    return effects.answer(schemas.IssueWritten, issue=fresh_issue_detail(session, issue, zone))


def path_dependency(dep_id: str, session: DbSession) -> models.Issue:
    return resolve_issue(session, dep_id)


@router.delete('/{id}/dependencies/{dep_id}', status_code=status.HTTP_204_NO_CONTENT)
def remove_dependency(issue: IssueFromPath, depends_on: Annotated[models.Issue, Depends(path_dependency)], session: DbSession):
    edge = session.get(models.IssueDependency, (issue.id, depends_on.id))
    if edge is None:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail=f'#{issue.number} does not depend on #{depends_on.number}',
        )
    session.delete(edge)
    issue.updated_ts = dt.datetime.now(dt.UTC)
    session.commit()
    return Response(status_code=status.HTTP_204_NO_CONTENT)


@router.get('/{id}/comments', response_model=list[schemas.IssueComment], status_code=status.HTTP_200_OK)
def read_comments(issue: IssueFromPath, session: DbSession, limit: RowLimit = None):
    """The issue's comments, oldest first, so a thread reads in the order it was written."""
    query = (
        select(models.IssueComment)
        .where(models.IssueComment.issue_id == issue.id)
        .order_by(models.IssueComment.created_ts, models.IssueComment.id)
    )
    if limit is not None:
        query = query.limit(limit)
    return list(session.scalars(query).all())


@router.post('/{id}/comments', response_model=schemas.IssueCommentWritten, status_code=status.HTTP_201_CREATED)
def create_comment(issue: IssueFromPath, comment: schemas.IssueCommentCreate, session: DbSession, effects: Effects):
    db_comment = models.IssueComment(issue_id=issue.id, body=comment.body, author=comment.author)
    session.add(db_comment)
    issue.updated_ts = dt.datetime.now(dt.UTC)
    session.commit()
    session.refresh(db_comment)
    return effects.answer(schemas.IssueCommentWritten, comment=schemas.IssueComment.model_validate(db_comment))


@router.delete('/{id}/comments/{comment_id}', status_code=status.HTTP_204_NO_CONTENT)
def delete_comment(issue: IssueFromPath, comment_id: UUID, session: DbSession):
    deleted = session.execute(
        sql_delete(models.IssueComment)
        .where(models.IssueComment.id == comment_id, models.IssueComment.issue_id == issue.id)
        .returning(models.IssueComment.id)
    ).scalar_one_or_none()
    if deleted is None:
        raise NotFoundException('comment', str(comment_id), logger)
    session.commit()
    return Response(status_code=status.HTTP_204_NO_CONTENT)
