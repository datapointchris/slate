"""Fixtures every suite shares: the session's private Postgres and API clients whose writes roll back."""

from collections.abc import Callable
from contextlib import contextmanager

import pytest
from fastapi.testclient import TestClient
from sqlalchemy.orm import Session

from slate.api import API_PREFIX
from slate.api.callers import get_current_caller
from slate.api.main import create_api
from slate.api.oidc_auth import OIDCIdentity
from slate.config import get_settings
from slate.database.session import get_db_engine
from slate.database.session import get_sqlalchemy_session
from tests.environment import PrivateServices
from tests.session_database import get_test_runner_settings
from tests.session_database import get_transactional_session
from tests.session_database import set_test_connection
from tests.session_database import test_settings

# A test names a resource route below the prefix: `/issues` reaches `/api/v1/issues`.
API_BASE_URL = f'http://testserver{API_PREFIX}'

# The person a logged-in client calls as. Their `sub` is what a close naming no actor records.
TEST_PERSON = OIDCIdentity(subject='test-person', client_id='slate-cli-test')


@pytest.fixture(scope='session', autouse=True)
def private_services():
    with PrivateServices(test_settings) as services:
        yield services


@pytest.fixture(scope='session')
def api(private_services):
    return create_api(settings=test_settings)


@contextmanager
def transactional_api_client(api, caller: Callable[[], OIDCIdentity] | None):
    """A client and a session sharing one transaction, which is rolled back when the test ends."""
    connection = get_db_engine(test_settings).connect()
    transaction = connection.begin()
    set_test_connection(connection)
    session = Session(bind=connection, join_transaction_mode='create_savepoint')
    original_overrides = api.dependency_overrides.copy()
    api.dependency_overrides[get_sqlalchemy_session] = get_transactional_session
    api.dependency_overrides[get_settings] = get_test_runner_settings
    if caller is not None:
        api.dependency_overrides[get_current_caller] = caller
    try:
        yield TestClient(api, base_url=API_BASE_URL), session
    finally:
        api.dependency_overrides = original_overrides
        session.close()
        set_test_connection(None)
        transaction.rollback()
        connection.close()


@pytest.fixture
def txn_api(api):
    """A client with no caller set: the real token chain runs, so a test overrides `get_oidc_identity`."""
    with transactional_api_client(api, caller=None) as client_and_session:
        yield client_and_session


@pytest.fixture
def txn_api_logged_in(api):
    """A client calling as `TEST_PERSON`."""
    with transactional_api_client(api, caller=lambda: TEST_PERSON) as client_and_session:
        yield client_and_session
