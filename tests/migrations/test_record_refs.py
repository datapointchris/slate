"""A ref filed with an issue or an idea is found by its prefix, and an idea's evidence migrates into refs."""

from alembic import command
from fastapi import status
from sqlalchemy import text

from tests.conftest import TEST_PERSON
from tests.conftest import transactional_api_client
from tests.util import show_status_and_response

# The revision before the one that carries refs on every record.
BEFORE_REFS = '34d106f9667d'

SEED_IDEA_WITH_EVIDENCE = """
WITH record AS (INSERT INTO records (kind, rank) VALUES ('idea', 1.0) RETURNING number),
idea AS (INSERT INTO ideas (id, number, title) SELECT gen_random_uuid(), number, 'Filed before refs' FROM record RETURNING id)
INSERT INTO idea_evidence (idea_id, ref, note, linked_by, linked_ts)
SELECT id, ref, note, 'claude-code/a1b2', linked_ts FROM idea, (VALUES
    ('capture/seeded', 'Where it came up', timestamptz '2026-11-01T00:00:00Z'),
    ('https://example.com/thread/9', NULL, timestamptz '2026-11-02T00:00:00Z')
) AS evidence (ref, note, linked_ts)
"""


def ok(response, expected=status.HTTP_200_OK) -> dict:
    assert response.status_code == expected, show_status_and_response(response)
    return response.json()


def carried(record: dict) -> list[tuple[str, str, str | None, str | None]]:
    return [(ref['ref'], ref['kind'], ref['note'], ref['linked_by']) for ref in record['refs']]


def test_a_ref_filed_with_a_record_is_found_by_its_prefix(scratch_database, api):
    config, engine = scratch_database
    command.upgrade(config, BEFORE_REFS)
    with engine.begin() as connection:
        connection.execute(text(SEED_IDEA_WITH_EVIDENCE))
    command.upgrade(config, 'head')

    with transactional_api_client(api, caller=lambda: TEST_PERSON, engine=engine) as (client, _):
        seeded = ok(client.get('/ideas'))[0]
        assert carried(seeded) == [
            ('capture/seeded', 'key', 'Where it came up', 'claude-code/a1b2'),
            ('https://example.com/thread/9', 'url', None, 'claude-code/a1b2'),
        ]
        assert seeded['evidence'] == seeded['refs']

        issue = ok(client.post('/issues', json={'title': 'Capture keeps the pane', 'refs': ['capture/issue']}), status.HTTP_201_CREATED)
        idea = ok(client.post('/ideas', json={'title': 'Capture keeps the font', 'refs': ['capture/idea']}), status.HTTP_201_CREATED)
        assert carried(issue['issue']) == [('capture/issue', 'key', None, TEST_PERSON.actor)]
        assert carried(idea['idea']) == [('capture/idea', 'key', None, TEST_PERSON.actor)]
        assert idea['idea']['evidence'] == idea['idea']['refs']

        assert [row['number'] for row in ok(client.get('/issues', params={'ref': 'capture/'}))] == [issue['issue']['number']]
        found = ok(client.get('/ideas', params={'ref': 'capture/'}))
        assert {row['number'] for row in found} == {seeded['number'], idea['idea']['number']}
        assert [row['number'] for row in ok(client.get('/ideas', params={'ref': 'capture/idea'}))] == [idea['idea']['number']]

        promoted = ok(client.post(f'/ideas/{idea["idea"]["number"]}/promote', json={'summary': 'The font is kept'}))
        initiative = promoted['idea']['initiative']
        assert ('refs_carried', initiative['number']) in [(effect['code'], effect['record']) for effect in promoted['side_effects']]
        assert carried(initiative) == carried(idea['idea'])
        assert [row['number'] for row in ok(client.get('/initiatives', params={'ref': 'capture/'}))] == [initiative['number']]
