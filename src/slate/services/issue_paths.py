"""Which held issues change the same code as another issue, read from the paths each names.

A path is a file or a directory in the issue's repo, and a directory covers everything under it,
as a git pathspec without wildcards does. Two paths overlap when they are equal or one is a
directory holding the other. Two issues overlap when they are on the same repo and any of their
paths do. An issue naming no path overlaps nothing.
"""

from __future__ import annotations

import datetime as dt
from collections.abc import Iterable
from dataclasses import dataclass

from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate.services.claim_views import live_claims


def paths_overlap(a: str, b: str) -> bool:
    return a == b or a.startswith(b + '/') or b.startswith(a + '/')


def meeting(mine: Iterable[str], theirs: Iterable[str]) -> list[str]:
    """Each of `theirs` that overlaps any of `mine`."""
    mine = list(mine)
    return sorted({path for path in theirs if any(paths_overlap(own, path) for own in mine)})


@dataclass(frozen=True)
class HeldOverlap:
    """Another issue under a live claim, and its paths that meet the issue asked about."""

    number: int
    title: str
    claim: models.Claim
    paths: list[str]


def held_overlaps(session: Session, issues: Iterable[models.Issue], now: dt.datetime) -> dict[int, list[HeldOverlap]]:
    """For each issue naming a path, every other issue under a live claim whose paths meet it, by number."""
    asking = [issue for issue in issues if issue.paths and issue.repo is not None]
    if not asking:
        return {}
    repos = {issue.repo for issue in asking}
    held = session.execute(
        select(models.Issue.number, models.Issue.title, models.Issue.repo, models.Issue.paths)
        .join(models.Claim, models.Claim.record_number == models.Issue.number)
        .where(models.Claim.is_live(now), models.Issue.repo.in_(repos), models.Issue.paths != [])
    ).all()
    claims = live_claims(session, [row.number for row in held], now)
    overlaps: dict[int, list[HeldOverlap]] = {}
    for issue in asking:
        for row in held:
            if row.number == issue.number or row.repo != issue.repo:
                continue
            if paths := meeting(issue.paths, row.paths):
                overlaps.setdefault(issue.number, []).append(HeldOverlap(row.number, row.title, claims[row.number], paths))
    return overlaps
