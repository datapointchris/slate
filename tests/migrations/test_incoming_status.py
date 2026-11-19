"""A filing held at `triage` reads `incoming` once migrated, and a writer still sending `triage` lands there too."""

from alembic import command
from fastapi import status
from sqlalchemy import text

from tests.conftest import TEST_PERSON
from tests.conftest import transactional_api_client
from tests.util import show_status_and_response

# The revision before the one that moves every `triage` issue to `incoming`.
BEFORE_INCOMING = '6d88eaec7184'

FILE_INTO_TRIAGE = """
WITH record AS (INSERT INTO records (kind, rank) VALUES ('issue', 1.0) RETURNING number)
INSERT INTO issues (id, number, title, status) SELECT gen_random_uuid(), number, 'Filed by a hook', 'triage' FROM record
"""


def ok(response, code=status.HTTP_200_OK):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()


def titles(client, issue_status: str) -> list[str]:
    return [row['title'] for row in ok(client.get('/issues', params={'status': issue_status}))]


def test_a_triage_filing_reads_incoming_after_the_migration(scratch_database, api):
    config, engine = scratch_database
    command.upgrade(config, BEFORE_INCOMING)
    with engine.begin() as connection:
        connection.execute(text(FILE_INTO_TRIAGE))

    command.upgrade(config, 'head')

    with engine.connect() as connection:
        assert connection.scalar(text("SELECT count(*) FROM issues WHERE status = 'triage'")) == 0
    with transactional_api_client(api, caller=lambda: TEST_PERSON, engine=engine) as (client, _):
        assert titles(client, 'incoming') == ['Filed by a hook']
        filed = ok(client.post('/issues', json={'title': 'Filed by an older CLI', 'status': 'triage'}), status.HTTP_201_CREATED)
        assert filed['issue']['status'] == 'incoming'
        assert titles(client, 'triage') == ['Filed by a hook', 'Filed by an older CLI']
