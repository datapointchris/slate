"""Narrowings an issue list and the ready queue share."""

from sqlalchemy import select
from sqlalchemy.orm import Session
from sqlalchemy.sql import Select

from slate import models
from slate.services.issue_readiness import IssueReadiness
from slate.services.record_refs import resolve_label


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


def autofixable_filtered(issues: list[models.Issue], readiness: IssueReadiness, autofixable: bool | None) -> list[models.Issue]:
    """Autofixable is derived on every read, so it narrows rows already read rather than the query."""
    if autofixable is None:
        return issues
    return [issue for issue in issues if (issue.id in readiness.autofixable) == autofixable]
