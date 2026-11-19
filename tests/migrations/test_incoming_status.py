"""A filing left at `triage` reads `incoming` once migrated, and a write or filter sending `triage` is refused, naming `incoming`."""

from alembic import command
from fastapi import status
from sqlalchemy import Engine
from sqlalchemy import text

from tests.conftest import TEST_PERSON
from tests.conftest import transactional_api_client
from tests.util import show_status_and_response

# The revision before the one that removes the status `triage`.
BEFORE_TRIAGE_IS_REFUSED = '74d59b6f8b72'

# A filing at `triage` the trigger would have stored as `incoming`, as a writer it missed would leave one.
FILE_INTO_TRIAGE = """
WITH record AS (INSERT INTO records (kind, rank) VALUES ('issue', 1.0) RETURNING number)
INSERT INTO issues (id, number, title, status) SELECT gen_random_uuid(), number, :title, 'triage' FROM record
"""


def file_into_triage(engine: Engine, title: str) -> None:
    with engine.begin() as connection:
        connection.execute(text('ALTER TABLE issues DISABLE TRIGGER store_triage_as_incoming'))
        connection.execute(text(FILE_INTO_TRIAGE), {'title': title})


def test_triage_is_refused_once_migrated(scratch_database, api):
    config, engine = scratch_database
    command.upgrade(config, BEFORE_TRIAGE_IS_REFUSED)
    file_into_triage(engine, 'Left at triage')
    command.upgrade(config, 'head')

    with engine.connect() as connection:
        assert connection.scalar(text("SELECT count(*) FROM issues WHERE status = 'triage'")) == 0
    with transactional_api_client(api, caller=lambda: TEST_PERSON, engine=engine) as (client, _):
        incoming = client.get('/issues', params={'status': 'incoming'}).json()
        assert [row['title'] for row in incoming] == ['Left at triage']
        for response in (
            client.post('/issues', json={'title': 'Filed by an older CLI', 'status': 'triage'}),
            client.patch(f'/issues/{incoming[0]["number"]}', json={'status': 'triage'}),
            client.get('/issues', params={'status': 'triage'}),
        ):
            assert response.status_code == status.HTTP_422_UNPROCESSABLE_CONTENT, show_status_and_response(response)
            assert 'incoming' in response.json()['message'], show_status_and_response(response)
