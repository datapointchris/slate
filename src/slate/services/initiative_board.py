"""Every initiative's counts, closes and slim issue rows in one read, for a board that polls it.

The read costs the same number of queries however many initiatives it answers. One readiness pass,
one read of the live claims and one read of the issues serve them all, and everything else folds
those rows. So it builds no `issue_views` row, which joins what a board does not draw.

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
    """The moment closes are counted back from, and the days they are counted on in the reader's zone."""

    now: dt.datetime
    zone: ZoneInfo
    days: list[dt.date]


def closes_of(issues: Iterable[schemas.BoardIssue], calendar: CloseCalendar) -> schemas.BoardCloses:
    moments = [issue.closed_ts for issue in issues if issue.state == 'completed' and issue.closed_ts is not None]
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
    today, now = calendar_now(zone)
    days = [today - dt.timedelta(days=back) for back in reversed(range(CLOSES_BY_DAY))]
    calendar = CloseCalendar(now, ZoneInfo(zone), days)

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

    rows = [board_initiative(initiative, issues_in[initiative.id], claims, calendar) for initiative in initiatives]
    return schemas.InitiativeBoard(
        read_ts=now,
        zone=zone,
        days=days,
        totals=schemas.BoardRollup(**rollup_of(rows, calendar)),
        tiers=[
            schemas.BoardTier(priority=priority, **rollup_of([row for row in rows if row.priority == priority], calendar))
            for priority in sorted(PRIORITIES, key=urgency)
        ],
        initiatives=rows,
    )


def board_initiative(
    initiative: models.Initiative,
    issues: list[schemas.BoardIssue],
    claims: dict[int, models.Claim],
    calendar: CloseCalendar,
) -> schemas.BoardInitiative:
    claim = claims.get(initiative.number)
    return schemas.BoardInitiative(
        number=initiative.number,
        name=initiative.name,
        status=initiative.status,
        priority=initiative.priority,
        repos=sorted({issue.repo for issue in issues if issue.repo is not None and issue.state != 'canceled'}),
        pinned_ts=initiative.pinned_ts,
        pinned_by=initiative.pinned_by,
        claim=claim_view(claim, calendar.now) if claim is not None else None,
        created_ts=initiative.created_ts,
        closed_ts=initiative.closed_ts,
        issues=issues,
        **counts_of(issues, calendar),
    )


def initiative_board_text(session: Session, initiative_status: str) -> schemas.InitiativeBoardText:
    """The long text of the initiatives at `initiative_status`, in the board's order."""
    columns = select(
        models.Initiative.id,
        models.Initiative.number,
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
                description=initiative.description,
                excerpt=description_excerpt(initiative.description),
                status_reason=initiative.status_reason,
                issues=texts_in[initiative.id],
            )
            for initiative in initiatives
        ],
    )
