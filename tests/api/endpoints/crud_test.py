from fastapi import status
from fastapi.testclient import TestClient
from pydantic import BaseModel

from tests.util import show_status_and_response


class ApiCrudTester[SchemaType: BaseModel]:
    """Create, read and delete one row through an endpoint, counting the list at each step.

    `written` is the key the create's answer holds the row under, beside its side effects.
    """

    def __init__(
        self,
        endpoint: str,
        new_obj: SchemaType,
        written: str,
        verify_attr: str = 'name',
        expected_length: int = 3,
        list_params: dict | None = None,
    ):
        self.endpoint = endpoint
        self.new_obj = new_obj
        self.written = written
        self.verify_attr = verify_attr
        self.expected_length = expected_length
        # Sent on the collection reads only, so a resource whose list narrows by
        # default can still be CRUD-tested against every row it holds. Without
        # it the counts here measure the filter rather than the create or the
        # delete. Not sent to the item reads, which take a path and no query.
        self.list_params = list_params

    def _list(self, test_api_client: TestClient):
        response = test_api_client.get(self.endpoint, params=self.list_params)
        assert response.status_code == status.HTTP_200_OK, show_status_and_response(response)
        return response.json()

    def _verify_length(self, rows: list[dict], expected_length: int):
        shown = [row.get(self.verify_attr) for row in rows]
        assert len(rows) == expected_length, f'{self.endpoint} should list {expected_length} rows but listed {len(rows)}: {shown}'

    def _verify_attribute(self, row: dict):
        expected = getattr(self.new_obj, self.verify_attr)
        assert row[self.verify_attr] == expected, f'{self.verify_attr} should be {expected}'

    def test_lifecycle(self, test_api_client: TestClient):
        self._verify_length(self._list(test_api_client), self.expected_length)

        created = test_api_client.post(self.endpoint, json=self.new_obj.model_dump(mode='json'))
        assert created.status_code == status.HTTP_201_CREATED, show_status_and_response(created)
        row = created.json()[self.written]
        self._verify_attribute(row)

        response = test_api_client.get(f'{self.endpoint}/{row["id"]}')
        assert response.status_code == status.HTTP_200_OK, show_status_and_response(response)
        self._verify_attribute(response.json())
        self._verify_length(self._list(test_api_client), self.expected_length + 1)

        deleted = test_api_client.delete(f'{self.endpoint}/{row["id"]}')
        assert deleted.status_code == status.HTTP_204_NO_CONTENT, show_status_and_response(deleted)
        self._verify_length(self._list(test_api_client), self.expected_length)
