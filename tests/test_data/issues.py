import datetime as dt

from slate.models import Initiative
from slate.models import Issue
from slate.models import IssueLabel

# Three ready issues, one decision, one in triage and one completed. The default
# list reads the five unclosed ones, and the ready queue the first three.
BASE_DATA: list[Issue] = [
    Issue(
        title='Ready bug with high priority',
        description='Repro is in the attached log',
        acceptance='The regression test passes',
        repo='webapp',
        type='bug',
        status='open',
        priority=2,
        rank=1.0,
    ),
    Issue(title='Ready task without priority', repo='dotfiles', type='task', status='open', priority=0, rank=2.0),
    Issue(title='Ready chore with low priority', repo=None, type='chore', status='open', priority=4, rank=3.0),
    Issue(title='Decision waiting on a person', repo='webapp', type='decision', status='open', priority=3, rank=4.0),
    Issue(title='Filed by a hook into triage', repo='dotfiles', type='chore', status='triage', priority=0, rank=5.0),
    Issue(
        title='Completed feature',
        repo='webapp',
        type='feature',
        status='completed',
        priority=2,
        rank=6.0,
        closed_ts=dt.datetime(2026, 9, 1, 12, 0, tzinfo=dt.UTC),
    ),
]

INITIATIVES: list[Initiative] = [
    Initiative(
        name='Ship the issue tracker',
        description='Issues apart from projects',
        priority=2,
        position=1,
        created_ts=dt.datetime(2026, 6, 1, 12, 0, tzinfo=dt.UTC),
    ),
    Initiative(name='Retire the old capture path', priority=0, position=2, created_ts=dt.datetime(2026, 7, 1, 12, 0, tzinfo=dt.UTC)),
    Initiative(name='Finished migration', status='completed', position=3, closed_ts=dt.datetime(2026, 8, 1, 12, 0, tzinfo=dt.UTC)),
]

LABELS: list[IssueLabel] = [
    IssueLabel(slug='area-api', group_slug='area', description='FastAPI endpoints'),
    IssueLabel(slug='area-cli', group_slug='area', description='The Go CLI'),
    IssueLabel(slug='needs-design', group_slug=None, description='The shape is not settled'),
]
