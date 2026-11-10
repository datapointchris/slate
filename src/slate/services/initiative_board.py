"""Every initiative's counts, closes and slim issue rows in one read, for a board that polls it.

The read costs the same number of queries however many initiatives it answers. One readiness pass,
one read of the live claims, one read of the issues and the read of the checked steps serve them all,
and everything else folds those rows. So it builds no `issue_views` row, which joins what a board does
not draw.

Long text is a second read, so a poll of the counts never carries a description.
"""

from __future__ import annotations

import datetime as dt
import re
from collections import Counter
from collections import defaultdict
from collections.abc import Iterable
from collections.abc import Sequence
from dataclasses import dataclass
from typing import Any
from zoneinfo import ZoneInfo

from sqlalchemy import select
from sqlalchemy.orm import Session
from sqlalchemy.orm import defer
from sqlalchemy.orm import load_only

from slate import models
from slate import schemas
from slate.models.record import PRIORITIES
from slate.services.claim_views import claim_view
from slate.services.claim_views import live_claims_on_every_record
from slate.services.initiative_progress import issue_state
from slate.services.initiatives import listed_initiatives
from slate.services.issue_readiness import IssueReadiness
from slate.services.issue_readiness import measure_readiness
from slate.services.issue_readiness import urgency
from slate.services.issue_views import calendar_now
from slate.services.issue_views import list_order
from slate.services.record_directions import directions_of
from slate.services.record_steps import initiative_steps
from slate.services.record_steps import stage_of
from slate.services.record_tags import own_tags

CLOSES_BY_DAY = 14

EXCERPT_CHARS = 300

FENCED_CODE = re.compile(r'^(`{3,}|~{3,}).*?^\1\s*$', re.DOTALL | re.MULTILINE)
BLANK_LINE = re.compile(r'\n\s*\n')
# A heading, a table row, a rule, a fence left unclosed or raw HTML, none of which reads as a sentence.
NOT_PROSE_LINE = re.compile(r'\s*(#{1,6}\s|\||-{3,}\s*$|\*{3,}\s*$|`{3,}|~{3,}|<)')
# A quote, a bullet, a number or a checkbox opening a line.
LINE_OPENING = re.compile(r'\s*(>\s*)*((?:[-*+]|\d+[.)])\s+)?(\[[ xX]\]\s+)?')
LINK = re.compile(r'!?\[([^\]]*)\]\([^)]*\)')
EMPHASIS = re.compile(r'\*(\S(?:[^*]*\S)?)\*')
INLINE_MARKUP = re.compile(r'\*\*|~~|`')


def description_excerpt(description: str | None) -> str | None:
    """The first paragraph holding prose, with its markdown stripped, cut at a word near `EXCERPT_CHARS`."""
    for block in BLANK_LINE.split(FENCED_CODE.sub('', description or '')):
        lines = (LINE_OPENING.sub('', line, count=1) for line in block.splitlines() if not NOT_PROSE_LINE.match(line))
        if prose := ' '.join(line.strip() for line in lines if line.strip()):
            return cut_at_word(EMPHASIS.sub(r'\1', INLINE_MARKUP.sub('', LINK.sub(r'\1', prose))))
    return None


def cut_at_word(text: str) -> str:
    if len(text) <= EXCERPT_CHARS:
        return text
    return text[:EXCERPT_CHARS].rsplit(' ', 1)[0].rstrip(' ,;:') + '…'


@dataclass(frozen=True)
class CloseCalendar:
    """The day and moment closes are counted back from, and the days they are counted on, in the reader's zone."""

    today: dt.date
    now: dt.datetime
    zone: ZoneInfo
    days: list[dt.date]

    @classmethod
    def as_of(cls, zone: str) -> CloseCalendar:
        """Now, and the last `CLOSES_BY_DAY` days ending today, in `zone`."""
        today, now = calendar_now(zone)
        return cls(today, now, ZoneInfo(zone), [today - dt.timedelta(days=back) for back in reversed(range(CLOSES_BY_DAY))])


def close_moments(issues: Iterable[schemas.BoardIssue]) -> list[dt.datetime]:
    """When each completed issue closed. A canceled one is no close, and one closed at no recorded time is in no window."""
    return [issue.closed_ts for issue in issues if issue.state == 'completed' and issue.closed_ts is not None]


def closes_of(issues: Iterable[schemas.BoardIssue], calendar: CloseCalendar) -> schemas.BoardCloses:
    moments = close_moments(issues)
    on_day = Counter(moment.astimezone(calendar.zone).date() for moment in moments)

    def within(window: dt.timedelta) -> int:
        return sum(moment > calendar.now - window for moment in moments)

    return schemas.BoardCloses(
        last_24h=within(dt.timedelta(hours=24)),
        last_7d=within(dt.timedelta(days=7)),
        last_30d=within(dt.timedelta(days=30)),
        last_closed_ts=max(moments, default=None),
        by_day=[on_day[day] for day in calendar.days],
    )


def counts_of(issues: Sequence[schemas.BoardIssue], calendar: CloseCalendar) -> dict[str, Any]:
    """The fields of a `BoardCounts` over `issues`."""
    states = Counter(issue.state for issue in issues)
    return {
        'issue_count': len(issues),
        'open_count': len(issues) - states['completed'] - states['canceled'],
        'states': schemas.IssueStateCounts.model_validate(states),
        'closes': closes_of(issues, calendar),
    }


def rollup_of(initiatives: Sequence[schemas.BoardInitiative], calendar: CloseCalendar) -> dict[str, Any]:
    """The fields of a `BoardRollup` over `initiatives`."""
    issues = [issue for initiative in initiatives for issue in initiative.issues]
    return {'initiative_count': len(initiatives), **counts_of(issues, calendar)}


def issues_by_repo(issues: Sequence[schemas.BoardIssue]) -> dict[str | None, list[schemas.BoardIssue]]:
    """`issues` by the repo each names. No issues at all is one empty set on no repo."""
    by_repo: dict[str | None, list[schemas.BoardIssue]] = defaultdict(list)
    for issue in issues:
        by_repo[issue.repo].append(issue)
    return by_repo or {None: []}


def most_open_first(row: schemas.BoardRepoCounts | schemas.BoardRepoRollup) -> tuple:
    return (row.repo is None, -row.open_count, row.repo or '')


def repo_rollups(rows: Sequence[schemas.BoardInitiative], calendar: CloseCalendar) -> list[schemas.BoardRepoRollup]:
    """Each repo's issues over every initiative in `rows`, counting the initiatives with a cut there."""
    issues_in: dict[str | None, list[schemas.BoardIssue]] = defaultdict(list)
    initiatives_in: Counter[str | None] = Counter()
    for row in rows:
        for repo, issues in issues_by_repo(row.issues).items():
            issues_in[repo].extend(issues)
            initiatives_in[repo] += 1
    rollups = [
        schemas.BoardRepoRollup(repo=repo, initiative_count=initiatives_in[repo], **counts_of(issues, calendar))
        for repo, issues in issues_in.items()
    ]
    return sorted(rollups, key=most_open_first)


def board_issue(issue: models.Issue, claim: models.Claim | None, readiness: IssueReadiness) -> schemas.BoardIssue:
    return schemas.BoardIssue(
        number=issue.number,
        title=issue.title,
        state=issue_state(issue, claim, readiness),
        type=issue.type,
        priority=issue.priority,
        effective_priority=readiness.effective_priority.get(issue.id, issue.priority),
        repo=issue.repo,
        holder=claim.holder if claim is not None else None,
        closed_ts=issue.closed_ts,
    )


def initiative_board(session: Session, initiative_status: str, zone: str) -> schemas.InitiativeBoard:
    """The board of the initiatives at `initiative_status`, as of now, its calendar read in `zone`."""
    calendar = CloseCalendar.as_of(zone)
    today, now, days = calendar.today, calendar.now, calendar.days

    query = listed_initiatives(select(models.Initiative).options(defer(models.Initiative.description)), initiative_status, session)
    initiatives = session.scalars(query).all()
    readiness = measure_readiness(session, today, now)
    claims = live_claims_on_every_record(session, now)
    issues = session.scalars(
        select(models.Issue)
        .options(
            load_only(
                models.Issue.number,
                models.Issue.title,
                models.Issue.status,
                models.Issue.type,
                models.Issue.priority,
                models.Issue.repo,
                models.Issue.closed_ts,
                models.Issue.initiative_id,
                models.Issue.rank,
            )
        )
        .where(models.Issue.initiative_id.in_([initiative.id for initiative in initiatives]))
    ).all()

    issues_in: dict[Any, list[schemas.BoardIssue]] = defaultdict(list)
    for issue in sorted(issues, key=list_order(readiness)):
        issues_in[issue.initiative_id].append(board_issue(issue, claims.get(issue.number), readiness))

    steps = initiative_steps(session, initiatives)
    numbers = [initiative.number for initiative in initiatives]
    tags, directions = own_tags(session, numbers), directions_of(session, numbers)
    rows = [
        board_initiative(
            initiative,
            issues_in[initiative.id],
            claims,
            steps.get(initiative.number, []),
            tags.get(initiative.number, []),
            directions.get(initiative.number, []),
            calendar,
        )
        for initiative in initiatives
    ]
    return schemas.InitiativeBoard(
        read_ts=now,
        zone=zone,
        days=days,
        totals=schemas.BoardRollup(**rollup_of(rows, calendar)),
        tiers=[
            schemas.BoardTier(priority=priority, **rollup_of([row for row in rows if row.priority == priority], calendar))
            for priority in sorted(PRIORITIES, key=urgency)
        ],
        repos=repo_rollups(rows, calendar),
        initiatives=rows,
    )


def board_initiative(
    initiative: models.Initiative,
    issues: list[schemas.BoardIssue],
    claims: dict[int, models.Claim],
    steps: list[schemas.CheckedStep],
    tags: list[str],
    directions: list[schemas.DirectionSummary],
    calendar: CloseCalendar,
) -> schemas.BoardInitiative:
    claim = claims.get(initiative.number)
    return schemas.BoardInitiative(
        number=initiative.number,
        name=initiative.name,
        status=initiative.status,
        priority=initiative.priority,
        tags=tags,
        directions=directions,
        repos=sorted({issue.repo for issue in issues if issue.repo is not None and issue.state != 'canceled'}),
        pinned_ts=initiative.pinned_ts,
        pinned_by=initiative.pinned_by,
        claim=claim_view(claim, calendar.now) if claim is not None else None,
        created_ts=initiative.created_ts,
        closed_ts=initiative.closed_ts,
        issues=issues,
        by_repo=sorted(
            (schemas.BoardRepoCounts(repo=repo, **counts_of(cut, calendar)) for repo, cut in issues_by_repo(issues).items()),
            key=most_open_first,
        ),
        steps=steps,
        stage=stage_of(steps),
        **counts_of(issues, calendar),
    )


def initiative_board_text(session: Session, initiative_status: str) -> schemas.InitiativeBoardText:
    """The long text of the initiatives at `initiative_status`, in the board's order."""
    columns = select(
        models.Initiative.id,
        models.Initiative.number,
        models.Initiative.summary,
        models.Initiative.description,
        models.Initiative.status_reason,
    )
    initiatives = session.execute(listed_initiatives(columns, initiative_status, session)).all()
    issues = session.execute(
        select(models.Issue.initiative_id, models.Issue.number, models.Issue.description)
        .where(models.Issue.initiative_id.in_([initiative.id for initiative in initiatives]))
        .order_by(models.Issue.number)
    ).all()
    texts_in: dict[Any, list[schemas.BoardIssueText]] = defaultdict(list)
    for issue in issues:
        texts_in[issue.initiative_id].append(schemas.BoardIssueText(number=issue.number, excerpt=description_excerpt(issue.description)))
    return schemas.InitiativeBoardText(
        read_ts=dt.datetime.now(dt.UTC),
        initiatives=[
            schemas.BoardInitiativeText(
                number=initiative.number,
                summary=initiative.summary,
                description=initiative.description,
                status_reason=initiative.status_reason,
                issues=texts_in[initiative.id],
            )
            for initiative in initiatives
        ],
    )
