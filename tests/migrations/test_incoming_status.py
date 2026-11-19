"""A filing held at `triage` reads `incoming` once migrated, and a writer still sending `triage` lands there too."""

from alembic import command
from fastapi import status
from sqlalchemy import Engine
from sqlalchemy import text

from tests.conftest import TEST_PERSON
from tests.conftest import transactional_api_client
from tests.util import show_status_and_response

# The revision before the one that moves every `triage` issue to `incoming`.
BEFORE_INCOMING = '6d88eaec7184'

# A filing as the API one release behind writes it.
FILE_INTO_TRIAGE = """
WITH record AS (INSERT INTO records (kind, rank) VALUES ('issue', :rank) RETURNING number)
INSERT INTO issues (id, number, title, status) SELECT gen_random_uuid(), number, :title, 'triage' FROM record
"""


def ok(response, code=status.HTTP_200_OK):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()


def titles(client, issue_status: str) -> list[str]:
    return [row['title'] for row in ok(client.get('/issues', params={'status': issue_status}))]


def file_into_triage(engine: Engine, title: str, rank: float) -> None:
    with engine.begin() as connection:
        connection.execute(text(FILE_INTO_TRIAGE), {'title': title, 'rank': rank})


def test_a_triage_filing_reads_incoming_after_the_migration(scratch_database, api):
    config, engine = scratch_database
    command.upgrade(config, BEFORE_INCOMING)
    file_into_triage(engine, 'Filed before the migration', 1.0)

    command.upgrade(config, 'head')
    file_into_triage(engine, 'Filed by an older API', 2.0)

    with engine.connect() as connection:
        assert connection.scalar(text("SELECT count(*) FROM issues WHERE status = 'triage'")) == 0
    with transactional_api_client(api, caller=lambda: TEST_PERSON, engine=engine) as (client, _):
        assert titles(client, 'incoming') == ['Filed before the migration', 'Filed by an older API']
        filed = ok(client.post('/issues', json={'title': 'Filed by an older CLI', 'status': 'triage'}), status.HTTP_201_CREATED)
        assert filed['issue']['status'] == 'incoming'
        assert titles(client, 'triage') == ['Filed before the migration', 'Filed by an older API', 'Filed by an older CLI']
