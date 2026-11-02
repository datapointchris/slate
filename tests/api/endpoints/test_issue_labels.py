import pytest
from fastapi import status

from tests.session_database import insert_test_data_transactional
from tests.util import show_status_and_response

LABELS = '/issues/labels'


@pytest.fixture
def client(txn_api_logged_in):
    client, session = txn_api_logged_in
    insert_test_data_transactional(session, 'issue_labels')
    return client


def ok(response, code=status.HTTP_200_OK):
    assert response.status_code == code, show_status_and_response(response)
    return response.json()


def test_grouped_labels_list_first_with_their_open_counts(client):
    ok(client.post('/issues', json={'title': 'open', 'labels': ['needs-design']}), status.HTTP_201_CREATED)
    done = ok(client.post('/issues', json={'title': 'done', 'labels': ['needs-design']}), status.HTTP_201_CREATED)
    ok(client.patch(f'/issues/{done["number"]}', json={'status': 'completed'}))

    listed = ok(client.get(LABELS))
    assert [label['slug'] for label in listed] == ['area-api', 'area-cli', 'needs-design']
    assert listed[-1]['open_issue_count'] == 1


def test_create_read_and_delete(client):
    created = ok(client.post(LABELS, json={'slug': 'area-vue', 'group_slug': 'area'}), status.HTTP_201_CREATED)
    assert created == {'slug': 'area-vue', 'group_slug': 'area', 'description': None, 'open_issue_count': 0}
    assert ok(client.get(f'{LABELS}/area-vue'))['group_slug'] == 'area'
    assert client.delete(f'{LABELS}/area-vue').status_code == status.HTTP_204_NO_CONTENT
    assert client.get(f'{LABELS}/area-vue').status_code == status.HTTP_404_NOT_FOUND


def test_a_slug_exists_once(client):
    assert client.post(LABELS, json={'slug': 'area-api'}).status_code == status.HTTP_409_CONFLICT


@pytest.mark.parametrize('slug', ['Area-API', 'area_api', 'area--api', '-area'])
def test_a_slug_is_lowercase_words_joined_by_single_hyphens(client, slug):
    assert client.post(LABELS, json={'slug': slug}).status_code == status.HTTP_422_UNPROCESSABLE_CONTENT


def test_moving_a_label_into_a_group_an_issue_already_uses_is_refused(client):
    carrier = ok(client.post('/issues', json={'title': 'both', 'labels': ['area-api', 'needs-design']}), status.HTTP_201_CREATED)
    response = client.patch(f'{LABELS}/needs-design', json={'group_slug': 'area'})
    assert response.status_code == status.HTTP_409_CONFLICT, show_status_and_response(response)
    assert f'#{carrier["number"]}' in response.json()['message']


def test_deleting_a_label_takes_it_off_its_issues(client):
    carrier = ok(client.post('/issues', json={'title': 'labeled', 'labels': ['area-api']}), status.HTTP_201_CREATED)
    assert client.delete(f'{LABELS}/area-api').status_code == status.HTTP_204_NO_CONTENT
    assert ok(client.get(f'/issues/{carrier["number"]}'))['labels'] == []
