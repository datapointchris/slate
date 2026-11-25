"""An idea promoted before promotion kept its number folds into its initiative once migrated, and lists as one item."""

import datetime as dt

from alembic import command
from sqlalchemy import Connection
from sqlalchemy import func
from sqlalchemy import select
from sqlalchemy import text

from slate.models.record_change import declare_audit_trail
from tests.conftest import TEST_PERSON
from tests.conftest import transactional_api_client

# The revision before the one that folds the pairs.
BEFORE_THE_PAIRS_FOLD = '765f75367cd0'
FOLDING_REVISION = '86158442774b'
PROMOTED_TS = dt.datetime(2026, 11, 14, 15, 39, 7, tzinfo=dt.UTC)


def returned(connection: Connection, statement: str, **values):
    return connection.execute(text(statement), values).scalar_one()


def seed_pair(connection: Connection) -> tuple[int, int]:
    """An idea with a worklog, its ended claim, a comment, a ref, a tag and a pursuit, and the initiative it was promoted to.

    Promotion copied the idea's ref, tag and pursuit to the initiative, as it did before it kept the number.
    """
    pursuit = returned(connection, "INSERT INTO records (kind, rank) VALUES ('pursuit', 1) RETURNING number")
    pursuit_id = returned(
        connection,
        "INSERT INTO pursuits (id, number, name, summary) VALUES (gen_random_uuid(), :n, 'Ops', 'It runs') RETURNING id",
        n=pursuit,
    )
    tag = returned(connection, "INSERT INTO tags (name, description) VALUES ('security', 'What an attacker reaches') RETURNING id")
    idea = returned(connection, "INSERT INTO records (kind, rank) VALUES ('idea', 2) RETURNING number")
    idea_id = returned(
        connection,
        'INSERT INTO ideas (id, number, title, priority, created_ts)'
        " VALUES (gen_random_uuid(), :n, 'Rounds on a schedule', 2, :at) RETURNING id",
        n=idea,
        at=PROMOTED_TS - dt.timedelta(days=1),
    )
    initiative = returned(connection, "INSERT INTO records (kind, rank) VALUES ('initiative', 3) RETURNING number")
    connection.execute(
        text(
            'INSERT INTO initiatives (id, number, name, summary, priority, created_ts, idea_id)'
            " VALUES (gen_random_uuid(), :n, 'Rounds run on a schedule', 'They run.', 2, :at, :idea_id)"
        ),
        {'n': initiative, 'at': PROMOTED_TS, 'idea_id': idea_id},
    )
    for number in (idea, initiative):
        connection.execute(text("INSERT INTO record_refs (record_number, ref) VALUES (:n, 'inbox:4102')"), {'n': number})
        connection.execute(text('INSERT INTO record_tags (record_number, tag_id) VALUES (:n, :tag)'), {'n': number, 'tag': tag})
        connection.execute(text('INSERT INTO record_pursuits (record_number, pursuit_id) VALUES (:n, :p)'), {'n': number, 'p': pursuit_id})
    claim = returned(
        connection,
        'INSERT INTO claims (record_number, holder, started_ts, expires_ts, ended_ts, ending)'
        " VALUES (:n, 'claude-code/shape', :start, :end, :end, 'promoted') RETURNING id",
        n=idea,
        start=PROMOTED_TS - dt.timedelta(hours=2),
        end=PROMOTED_TS,
    )
    connection.execute(
        text(
            'INSERT INTO worklogs (record_number, sequence, claim_id, opened_by, started_ts, ended_ts, outcome, step)'
            " VALUES (:n, 1, :claim, 'claude-code/shape', :start, :end, 'promoted', 'shape')"
        ),
        {'n': idea, 'claim': claim, 'start': PROMOTED_TS - dt.timedelta(hours=2), 'end': PROMOTED_TS},
    )
    connection.execute(text("INSERT INTO comments (record_number, body) VALUES (:n, 'Worth a schedule')"), {'n': idea})
    return idea, initiative


def test_promoted_pairs_fold_into_their_initiatives(scratch_database, api):
    config, engine = scratch_database
    command.upgrade(config, BEFORE_THE_PAIRS_FOLD)
    with engine.begin() as connection:
        # The release before reached head, which attached the redirect tables' audit triggers.
        declare_audit_trail(connection)
    with engine.begin() as connection:
        connection.execute(select(func.set_config('audit.actor', 'chris', True)))
        idea, initiative = seed_pair(connection)
        last_seeded = returned(connection, 'SELECT max(id) FROM audit_trail')

    command.upgrade(config, 'head')

    with transactional_api_client(api, caller=lambda: TEST_PERSON, engine=engine) as (client, _):
        ended = client.get('/ideas', params={'state': 'ended', 'tag': 'security'}).json()
        listed = [item for item in ended if item['number'] in (idea, initiative)]
        assert [(item['number'], item['became']) for item in listed] == [
            (
                idea,
                {
                    'how': 'promoted',
                    'kind': 'initiative',
                    'number': initiative,
                    'at': PROMOTED_TS.isoformat().replace('+00:00', 'Z'),
                    'title': 'Rounds run on a schedule',
                    'status': 'active',
                },
            )
        ]
        assert client.get(f'/worklogs/{initiative}.1').json()['former_names'] == [f'{idea}.1']
        shown = client.get(f'/initiatives/{initiative}').json()
        assert ([claim['holder'] for claim in shown['claims']], [comment['body'] for comment in shown['comments']]) == (
            ['claude-code/shape'],
            ['Worth a schedule'],
        )
        assert (shown['refs'][0]['ref'], shown['tags'], [pursuit['name'] for pursuit in shown['pursuits']]) == (
            'inbox:4102',
            ['security'],
            ['Ops'],
        )

    with engine.connect() as connection:
        record = connection.execute(text('SELECT promoted_ts, promoted_by FROM records WHERE number = :n'), {'n': initiative}).one()
        assert tuple(record) == (PROMOTED_TS, 'chris')
        left = connection.execute(
            text(
                'SELECT (SELECT count(*) FROM record_refs WHERE record_number = :n)'
                ' + (SELECT count(*) FROM record_tags WHERE record_number = :n)'
                ' + (SELECT count(*) FROM record_pursuits WHERE record_number = :n)'
            ),
            {'n': idea},
        ).scalar_one()
        assert left == 0
        signers = (
            connection.execute(text('SELECT DISTINCT actor FROM audit_trail WHERE id > :seeded'), {'seeded': last_seeded}).scalars().all()
        )
        assert signers == [f'alembic/{FOLDING_REVISION}']
