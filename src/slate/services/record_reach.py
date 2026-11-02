"""The records a record's work reaches through to, which every total of that work counts.

A record always reaches itself. An issue reaches every issue under it, at any depth, because a
parent is work too large for one issue, split into its children. An initiative reaches the idea it
was promoted from, every issue in it, and every issue under those. A child does not take its
parent's initiative, so without that walk an issue's work could exceed its initiative's.

This is not membership. An issue is in an initiative when its `initiative_id` names it, which is
what an initiative's counts by status and the queue read. Reach is the wider set its work spans.
"""

from __future__ import annotations

from sqlalchemy import ColumnElement
from sqlalchemy import Select
from sqlalchemy import or_
from sqlalchemy import select
from sqlalchemy.orm import Session
from sqlalchemy.orm import aliased
from sqlalchemy.sql.elements import SQLCoreOperations

from slate import models
from slate.models.record import Kind


def issue_tree_numbers(roots: Select) -> Select:
    """The numbers of the issues whose ids `roots` selects, and of every issue under them.

    `UNION` rather than `UNION ALL`, so a cycle in the parent links ends the walk.
    """
    tree = select(models.Issue.id, models.Issue.number).where(models.Issue.id.in_(roots)).cte('issue_tree', recursive=True)
    child = aliased(models.Issue)
    tree = tree.union(select(child.id, child.number).join(tree, child.parent_id == tree.c.id))
    return select(tree.c.number)


def within_reach(session: Session, record: models.Record, number: SQLCoreOperations[int]) -> ColumnElement[bool]:
    """Whether `number` names the record or one the record's work reaches through to."""
    match record.kind:
        case Kind.IDEA:
            return number == record.number
        case Kind.ISSUE:
            return number.in_(issue_tree_numbers(select(models.Issue.id).where(models.Issue.number == record.number)))
        case Kind.INITIATIVE:
            initiative = session.scalars(select(models.Initiative).where(models.Initiative.number == record.number)).one()
            own = [initiative.number, *([initiative.idea.number] if initiative.idea is not None else [])]
            issues = select(models.Issue.id).where(models.Issue.initiative_id == initiative.id)
            return or_(number.in_(own), number.in_(issue_tree_numbers(issues)))
