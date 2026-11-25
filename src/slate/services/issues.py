"""Create issues, and the checks every create and edit of one makes."""

from fastapi import HTTPException
from fastapi import status
from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.models.initiative import TERMINAL_INITIATIVE_STATUSES
from slate.services.idea_promotion import PromotedIdea
from slate.services.idea_promotion import in_place_of
from slate.services.likely_duplicates import warn_likely_duplicates
from slate.services.record_lookup import resolve_initiative
from slate.services.record_lookup import resolve_issue
from slate.services.record_lookup import resolve_labels
from slate.services.record_refs import link_refs
from slate.services.record_tags import resolve_tags
from slate.services.record_tags import set_record_tags
from slate.services.vocabulary import validate_lookup
from slate.services.wait_cycles import read_wait_graph
from slate.services.wait_cycles import refuse_wait_loop
from slate.services.write_effects import WriteEffects

# A new issue starts in one of these. Working it reaches every later status.
CREATE_STATUSES = ('incoming', 'open')


def unprocessable(detail: str) -> HTTPException:
    return HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_CONTENT, detail=detail)


def ensure_initiative_open(initiative: models.Initiative) -> None:
    if initiative.status in TERMINAL_INITIATIVE_STATUSES:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'Initiative {initiative.name!r} is {initiative.status}. Reopen it before filing work into it.',
        )


def ensure_parent_open(parent: models.Issue) -> None:
    """A closed issue takes no unclosed children: a completed one would be finished with work still open."""
    if parent.is_closed:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f'#{parent.number} is {parent.status}. Reopen it before filing work under it.',
        )


def ensure_paths_have_a_repo(repo: str | None, paths: list[str]) -> None:
    """A path is read in the issue's repo, so on no repo it names nothing to compare."""
    if paths and repo is None:
        raise unprocessable(f"Paths are relative to the issue's repo, and this issue is on none: {', '.join(paths)}. Give it a repo.")


def set_labels(session: Session, issue: models.Issue, slugs: list[str]) -> None:
    """Make the issue's labels exactly `slugs`, keeping the assignment rows that stay."""
    wanted = {label.id for label in resolve_labels(session, slugs)}
    kept = [assignment for assignment in issue.label_assignments if assignment.label_id in wanted]
    held = {assignment.label_id for assignment in kept}
    issue.label_assignments = kept + [models.IssueLabelAssignment(label_id=label_id) for label_id in sorted(wanted - held)]


def new_issue(
    session: Session, issue: schemas.IssueCreate, linked_by: str, effects: WriteEffects, from_idea: PromotedIdea | None = None
) -> models.Issue:
    """Add an issue after every check a create makes, link its refs as `linked_by`, and warn of each open record it reads like.

    Its record goes last and takes its writer's flow, as every new one does. In place of an idea, it
    keeps the idea's record, `id` and `created_ts`, with the place, pin and flow the record holds.
    A record keeps its tags where `issue` names none, and a new one holds none.
    """
    if issue.status not in CREATE_STATUSES:
        raise unprocessable(f'A new issue starts as {" or ".join(CREATE_STATUSES)}, not {issue.status!r}')
    validate_lookup(session, models.IssueType, issue.type, 'issue type')
    ensure_paths_have_a_repo(issue.repo, issue.paths)
    tags = resolve_tags(session, issue.tags)

    db_issue = models.Issue(
        title=issue.title,
        description=issue.description,
        acceptance=issue.acceptance,
        repo=issue.repo,
        paths=issue.paths,
        type=issue.type,
        status=issue.status,
        priority=issue.priority,
        deferred_until_date=issue.deferred_until_date,
        autofix=issue.autofix,
        **in_place_of(from_idea),
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
    if 'tags' in issue.model_fields_set:
        set_record_tags(session, db_issue.number, tags)
    link_refs(session, db_issue.number, issue.refs, None, linked_by, effects)
    # Each edge that the new issue adds touches it, so any loop closed by them runs through it.
    graph = read_wait_graph(session)
    for blocker in depends_on:
        graph.add_dependency(db_issue.number, blocker.number)
    refuse_wait_loop(graph, db_issue.number)
    for blocker in depends_on:
        session.add(models.IssueDependency(issue_id=db_issue.id, depends_on_id=blocker.id))
    session.flush()
    warn_likely_duplicates(session, db_issue.number, db_issue.title, db_issue.repo, effects)
    return db_issue
