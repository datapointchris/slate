from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate.schemas.ready_filter import ReadyFilter
from slate.services.issue_filters import apply_label_filter
from slate.services.issue_filters import apply_repo_filter
from slate.services.issue_filters import autofixable_filtered
from slate.services.issue_readiness import IssueReadiness
from slate.services.issue_readiness import measure_readiness
from slate.services.issue_views import ISSUE_LOAD_OPTIONS
from slate.services.issue_views import calendar_now
from slate.services.record_refs import resolve_initiative
from slate.services.vocabulary import validate_lookup


def ready_issues(session: Session, zone: str, filters: ReadyFilter) -> tuple[list[models.Issue], IssueReadiness]:
    """The ready queue in the order it is taken, and the readiness it was cut from.

    Decisions wait on a person, so the queue leaves them out unless `type` asks
    for them by name.
    """
    today, now = calendar_now(zone)
    readiness = measure_readiness(session, today, now)
    query = select(models.Issue).options(*ISSUE_LOAD_OPTIONS).where(models.Issue.id.in_(readiness.ready))
    if filters.type is not None:
        validate_lookup(session, models.IssueType, filters.type, 'issue type')
        query = query.where(models.Issue.type == filters.type)
    else:
        query = query.where(models.Issue.type != 'decision')
    if filters.initiative is not None:
        query = query.where(models.Issue.initiative_id == resolve_initiative(session, filters.initiative).id)
    query = apply_repo_filter(query, filters.repo)
    query = apply_label_filter(query, filters.label, session)
    issues = sorted(session.scalars(query).all(), key=readiness.sort_key)
    return autofixable_filtered(issues, readiness, filters.autofixable), readiness
