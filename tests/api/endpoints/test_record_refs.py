"""A ref filed with an issue or an idea is found by its prefix, and a promotion carries it to the initiative."""

from fastapi import status

from tests.conftest import TEST_PERSON
from tests.util import show_status_and_response


def ok(response, expected=status.HTTP_200_OK) -> dict:
    assert response.status_code == expected, show_status_and_response(response)
    return response.json()


def carried(record: dict) -> list[tuple[str, str, str | None, str | None]]:
    return [(ref['ref'], ref['kind'], ref['note'], ref['linked_by']) for ref in record['refs']]


def test_a_ref_filed_with_a_record_is_found_by_its_prefix(txn_api_logged_in):
    client, _ = txn_api_logged_in
    issue = ok(client.post('/issues', json={'title': 'Capture keeps the pane', 'refs': ['capture/issue']}), status.HTTP_201_CREATED)
    idea = ok(client.post('/ideas', json={'title': 'Capture keeps the font', 'refs': ['capture/idea']}), status.HTTP_201_CREATED)
    other = ok(client.post('/ideas', json={'title': 'Capture keeps the theme', 'refs': ['capture/other']}), status.HTTP_201_CREATED)
    assert carried(issue['issue']) == [('capture/issue', 'key', None, TEST_PERSON.actor)]
    assert carried(idea['idea']) == [('capture/idea', 'key', None, TEST_PERSON.actor)]

    assert [row['number'] for row in ok(client.get('/issues', params={'ref': 'capture/'}))] == [issue['issue']['number']]
    found = ok(client.get('/ideas', params={'ref': 'capture/'}))
    assert {row['number'] for row in found} == {idea['idea']['number'], other['idea']['number']}
    assert [row['number'] for row in ok(client.get('/ideas', params={'ref': 'capture/idea'}))] == [idea['idea']['number']]

    promoted = ok(client.post(f'/ideas/{idea["idea"]["number"]}/promote', json={'summary': 'The font is kept'}))
    initiative = promoted['idea']['initiative']
    assert ('refs_carried', initiative['number']) in [(effect['code'], effect['record']) for effect in promoted['side_effects']]
    assert carried(initiative) == carried(idea['idea'])
    assert [row['number'] for row in ok(client.get('/initiatives', params={'ref': 'capture/'}))] == [initiative['number']]
