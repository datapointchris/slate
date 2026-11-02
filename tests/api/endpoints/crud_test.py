from fastapi import status
from fastapi.testclient import TestClient
from pydantic import BaseModel

from tests.util import show_status_and_response


class ApiCrudTester[SchemaType: BaseModel]:
    def __init__(
        self,
        endpoint: str,
        new_obj: SchemaType,
        verify_attr: str = 'name',
        expected_length: int = 3,
        list_params: dict | None = None,
    ):
        self.endpoint = endpoint
        self.new_obj = new_obj
        self.verify_attr = verify_attr
        self.expected_length = expected_length
        # Sent on the collection reads only, so a resource whose list narrows by
        # default can still be CRUD-tested against every row it holds. Without
        # it the counts here measure the filter rather than the create or the
        # delete. Not sent to the item reads, which take a path and no query.
        self.list_params = list_params

    def _list(self, test_api_client: TestClient):
        return test_api_client.get(self.endpoint, params=self.list_params)

    def _verify_length(self, response, expected_length: int):
        r_json = response.json()
        num_items_returned = len(r_json)
        endpoint_items = (
            f'{self.endpoint} {self.verify_attr}s: {[k.get(self.verify_attr) for k in r_json]}' if num_items_returned > 0 else ''
        )
        name = self.endpoint.strip('/').split('/')[-1]
        error_message = f"""{self.endpoint} should have {expected_length} {name} but had {num_items_returned}
            Request: {response.request.method} {self.endpoint}
            {endpoint_items}
            """

        assert num_items_returned == expected_length, error_message

    def _verify_attribute(self, response, verify_attr: str):
        verify_error_message = f'{verify_attr} should be {getattr(self.new_obj, verify_attr)}'
        assert dict(response.json())[verify_attr] == getattr(self.new_obj, verify_attr), verify_error_message

    def item_id_by_position(self, test_api_client: TestClient, position: int):
        """Get item ID by position in the list.

        This assumes the endpoint supports listing all items.
        """
        all_obj = self._list(test_api_client)
        assert all_obj.status_code == status.HTTP_200_OK, show_status_and_response(all_obj)
        return all_obj.json()[position - 1]['id']

    def get_item_by_attribute(self, test_api_client: TestClient, attr_name: str, attr_value: str) -> dict | None:
        """Get item by matching an attribute value - useful for finding specific items."""
        all_obj = self._list(test_api_client)
        if all_obj.status_code == status.HTTP_200_OK:
            items = all_obj.json()
            for item in items:
                if item.get(attr_name) == attr_value:
                    return item
        return None

    def test_read_one(self, test_api_client: TestClient):
        first_id = self.item_id_by_position(test_api_client, position=1)
        response = test_api_client.get(f'{self.endpoint}/{first_id}')
        assert response.status_code == status.HTTP_200_OK, show_status_and_response(response)

    def test_read_many(self, test_api_client: TestClient):
        response = self._list(test_api_client)
        assert response.status_code == status.HTTP_200_OK, show_status_and_response(response)
        self._verify_length(response, self.expected_length)

    def test_create(self, test_api_client: TestClient):
        created = test_api_client.post(self.endpoint, json=self.new_obj.model_dump(mode='json'))
        assert created.status_code == status.HTTP_201_CREATED, show_status_and_response(created)
        self._verify_attribute(created, self.verify_attr)

        all = self._list(test_api_client)
        assert all.status_code == status.HTTP_200_OK, show_status_and_response(all)
        self._verify_length(all, self.expected_length + 1)

    def test_delete(self, test_api_client: TestClient):
        all_before_delete = self._list(test_api_client)
        assert all_before_delete.status_code == status.HTTP_200_OK, show_status_and_response(all_before_delete)
        self._verify_length(all_before_delete, self.expected_length)

        first_id = all_before_delete.json()[0]['id']
        response = test_api_client.delete(f'{self.endpoint}/{first_id}')
        assert response.status_code == status.HTTP_204_NO_CONTENT, show_status_and_response(response)

        all_after_delete = self._list(test_api_client)
        assert all_after_delete.status_code == status.HTTP_200_OK, show_status_and_response(all_after_delete)
        self._verify_length(all_after_delete, self.expected_length - 1)

    def test_lifecycle(self, test_api_client: TestClient):
        original_objects = self._list(test_api_client)
        assert original_objects.status_code == status.HTTP_200_OK, show_status_and_response(original_objects)
        self._verify_length(original_objects, self.expected_length)

        created = test_api_client.post(self.endpoint, json=self.new_obj.model_dump(mode='json'))
        assert created.status_code == status.HTTP_201_CREATED, show_status_and_response(created)
        self._verify_attribute(created, self.verify_attr)

        created_id = created.json().get('id')
        response = test_api_client.get(f'{self.endpoint}/{created_id}')
        assert response.status_code == status.HTTP_200_OK, show_status_and_response(response)
        self._verify_attribute(response, self.verify_attr)

        all_obj_new = self._list(test_api_client)
        assert all_obj_new.status_code == status.HTTP_200_OK, show_status_and_response(all_obj_new)
        self._verify_length(all_obj_new, self.expected_length + 1)

        deleted = test_api_client.delete(f'{self.endpoint}/{created_id}')
        assert deleted.status_code == status.HTTP_204_NO_CONTENT, show_status_and_response(deleted)

        all_obj_after_delete = self._list(test_api_client)
        assert all_obj_after_delete.status_code == status.HTTP_200_OK, show_status_and_response(all_obj_after_delete)
        self._verify_length(all_obj_after_delete, self.expected_length)
