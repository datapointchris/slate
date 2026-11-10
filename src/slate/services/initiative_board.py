"""Every initiative's counts, closes and slim issue rows in one read, for a board that polls it.

The read costs the same number of queries however many initiatives it answers. One readiness pass,
one read of the live claims, one read of the issues, the read of the checked steps and one of when
each was last claimed serve them all, and everything else folds those rows. So it builds no
`issue_views` row, which joins what a board does not draw. The closes a direction or a tag counts
from initiatives off the board are one more read, bounded by the longest close window.

Long text is a second read, so a poll of the counts never carries a description.
"""

from __future__ import annotations

import datetime as dt
import re
from collections import Counter
from collections import defaultdict
from collections.abc import Callable
from collections.abc import Iterable
from collections.abc import Sequence
from dataclasses import dataclass
from dataclasses import field
from typing import Any
from zoneinfo import ZoneInfo

from sqlalchemy import func
from sqlalchemy import select
from sqlalchemy import union_all
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

# The widest window a close is counted in, which bounds the closes read from initiatives off the board.
LONGEST_CLOSE_WINDOW = dt.timedelta(days=30)

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
    return closes_at(close_moments(issues), calendar)


def closes_at(moments: Sequence[dt.datetime], calendar: CloseCalendar) -> schemas.BoardCloses:
    on_day = Counter(moment.astimezone(calendar.zone).date() for moment in moments)

    def within(window: dt.timedelta) -> int:
        return sum(moment > calendar.now - window for moment in moments)

    return schemas.BoardCloses(
        last_24h=within(dt.timedelta(hours=24)),
        last_7d=within(dt.timedelta(days=7)),
        last_30d=within(LONGEST_CLOSE_WINDOW),
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
    claimed = last_claimed(session, initiatives)
    rows = [
        board_initiative(
            initiative,
            issues_in[initiative.id],
            claims,
            steps.get(initiative.number, []),
            tags.get(initiative.number, []),
            directions.get(initiative.number, []),
            claimed.get(initiative.number),
            calendar,
        )
        for initiative in initiatives
    ]
    elsewhere = closes_off_the_board(session, initiatives, calendar)
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
        directions=direction_rollups(session, rows, elsewhere, calendar),
        tags=tag_rollups(session, rows, elsewhere, calendar),
        initiatives=rows,
    )


def last_claimed(session: Session, initiatives: Sequence[models.Initiative]) -> dict[int, dt.datetime]:
    """When a claim last started on each initiative or on one of its issues, by the initiative's number. Ended claims count."""
    ids = [initiative.id for initiative in initiatives]
    on_issues = (
        select(models.Initiative.number.label('initiative'), models.Claim.started_ts)
        .join(models.Issue, models.Issue.initiative_id == models.Initiative.id)
        .join(models.Claim, models.Claim.record_number == models.Issue.number)
        .where(models.Initiative.id.in_(ids))
    )
    on_initiatives = (
        select(models.Initiative.number.label('initiative'), models.Claim.started_ts)
        .join(models.Claim, models.Claim.record_number == models.Initiative.number)
        .where(models.Initiative.id.in_(ids))
    )
    claims = union_all(on_issues, on_initiatives).subquery()
    rows = session.execute(select(claims.c.initiative, func.max(claims.c.started_ts)).group_by(claims.c.initiative))
    return {number: started for number, started in rows}


def touched_at(initiative: models.Initiative, issues: Iterable[schemas.BoardIssue], claimed: dt.datetime | None) -> dt.datetime:
    """The latest of when it was created, when a claim on it or an issue started, and when an issue closed."""
    moments = [initiative.created_ts, *(issue.closed_ts for issue in issues if issue.closed_ts is not None)]
    return max(moments if claimed is None else [*moments, claimed])


@dataclass(frozen=True)
class ClosesElsewhere:
    """The issues completed inside the longest close window in each initiative off the board, with its directions and tags."""

    moments: dict[int, list[dt.datetime]]
    directions: dict[int, list[schemas.DirectionSummary]]
    tags: dict[int, list[str]]


def closes_off_the_board(session: Session, initiatives: Sequence[models.Initiative], calendar: CloseCalendar) -> ClosesElsewhere:
    """Each issue completed inside the longest window in an initiative the board does not hold, such as one completed since.

    The board holds the initiatives at one status, so a direction or a tag counts the rest's closes from here.
    """
    rows = session.execute(
        select(models.Initiative.number, models.Issue.closed_ts)
        .join(models.Issue, models.Issue.initiative_id == models.Initiative.id)
        .where(
            models.Issue.status == 'completed',
            models.Issue.closed_ts > calendar.now - LONGEST_CLOSE_WINDOW,
            models.Initiative.id.not_in([initiative.id for initiative in initiatives]),
        )
    )
    moments: dict[int, list[dt.datetime]] = defaultdict(list)
    for number, closed in rows:
        if closed is not None:
            moments[number].append(closed)
    return ClosesElsewhere(dict(moments), directions_of(session, moments), own_tags(session, moments))


@dataclass
class GroupTally:
    """The board's initiatives in one group, and the closes of the group's initiatives off the board."""

    rows: list[schemas.BoardInitiative] = field(default_factory=list)
    elsewhere: list[dt.datetime] = field(default_factory=list)


def tally_groups[Key](
    rows: Sequence[schemas.BoardInitiative],
    groups_of: Callable[[schemas.BoardInitiative], list[Key]],
    elsewhere: dict[int, list[dt.datetime]],
    elsewhere_in: dict[int, list[Key]],
    listed: Iterable[Key],
) -> dict[Key | None, GroupTally]:
    """Each group's tally: every group `listed`, each other group an initiative is in, and None for those in none, where any is.

    An initiative in two groups counts in both.
    """
    tallies: dict[Key | None, GroupTally] = {key: GroupTally() for key in listed}
    for row in rows:
        for key in in_none_where_empty(groups_of(row)):
            tallies.setdefault(key, GroupTally()).rows.append(row)
    for number, moments in elsewhere.items():
        for key in in_none_where_empty(elsewhere_in.get(number, [])):
            tallies.setdefault(key, GroupTally()).elsewhere.extend(moments)
    return tallies


def in_none_where_empty[Key](keys: list[Key]) -> list[Key | None]:
    return [*keys] if keys else [None]


def grouping_rollup(tally: GroupTally, calendar: CloseCalendar) -> dict[str, Any]:
    """The fields of a `BoardGroupingRollup` over `tally`."""
    on_the_board = close_moments(issue for row in tally.rows for issue in row.issues)
    active = [row for row in tally.rows if row.status == 'active']
    untouched = min(active, key=lambda row: row.touched_ts, default=None)
    return {
        **rollup_of(tally.rows, calendar),
        'closes': closes_at([*on_the_board, *tally.elsewhere], calendar),
        'untouched_longest': None
        if untouched is None
        else schemas.BoardUntouched(number=untouched.number, name=untouched.name, touched_ts=untouched.touched_ts),
    }


def fewest_closes_first(rollup: schemas.BoardGroupingRollup, name: str | None) -> tuple:
    return (name is None, rollup.closes.last_30d, name or '')


def direction_rollups(
    session: Session, rows: Sequence[schemas.BoardInitiative], elsewhere: ClosesElsewhere, calendar: CloseCalendar
) -> list[schemas.BoardDirectionRollup]:
    """Every active direction, each other one an initiative counted is in, and the initiatives in none, the fewest closes first."""
    named = dict(session.execute(select(models.Direction.number, models.Direction.name).where(models.Direction.status == 'active')).all())
    for direction in (*(d for row in rows for d in row.directions), *(d for held in elsewhere.directions.values() for d in held)):
        named[direction.number] = direction.name
    tallies = tally_groups(
        rows,
        lambda row: [direction.number for direction in row.directions],
        elsewhere.moments,
        {number: [direction.number for direction in held] for number, held in elsewhere.directions.items()},
        named,
    )
    rollups = [
        schemas.BoardDirectionRollup(
            direction=None if number is None else schemas.DirectionSummary(number=number, name=named[number]),
            **grouping_rollup(tally, calendar),
        )
        for number, tally in tallies.items()
    ]
    return sorted(rollups, key=lambda rollup: fewest_closes_first(rollup, rollup.direction.name if rollup.direction else None))


def tag_rollups(
    session: Session, rows: Sequence[schemas.BoardInitiative], elsewhere: ClosesElsewhere, calendar: CloseCalendar
) -> list[schemas.BoardTagRollup]:
    """Every tag, and the initiatives carrying none of their own, the fewest closes first."""
    tallies = tally_groups(rows, lambda row: row.tags, elsewhere.moments, elsewhere.tags, session.scalars(select(models.Tag.name)).all())
    rollups = [schemas.BoardTagRollup(tag=tag, **grouping_rollup(tally, calendar)) for tag, tally in tallies.items()]
    return sorted(rollups, key=lambda rollup: fewest_closes_first(rollup, rollup.tag))


def board_initiative(
    initiative: models.Initiative,
    issues: list[schemas.BoardIssue],
    claims: dict[int, models.Claim],
    steps: list[schemas.CheckedStep],
    tags: list[str],
    directions: list[schemas.DirectionSummary],
    claimed: dt.datetime | None,
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
        touched_ts=touched_at(initiative, issues, claimed),
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
