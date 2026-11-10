"""What the API answers while the Postgres it reached once is down."""

import copy
import socket
from collections.abc import Generator

from fastapi.testclient import TestClient
from sqlalchemy.orm import Session
from testcontainers.community.postgres import PostgresContainer

from slate.api import API_PREFIX
from slate.api.callers import get_current_caller
from slate.api.main import create_api
from slate.database.session import create_session
from slate.database.session import get_db_engine
from slate.database.session import get_sqlalchemy_session
from tests.conftest import TEST_PERSON
from tests.environment import POSTGRES_DB
from tests.environment import POSTGRES_PASSWORD
from tests.environment import POSTGRES_USER
from tests.environment import compose_postgres_image
from tests.session_database import test_settings


def unused_port() -> int:
    with socket.socket() as probe:
        probe.bind(('', 0))
        return probe.getsockname()[1]


def test_postgres_stopping_after_its_first_answer_leaves_the_api_ready_and_answering_503():
    # A fixed port, so the API's address refuses before the container starts and after it stops.
    port = unused_port()
    postgres = PostgresContainer(
        compose_postgres_image(), username=POSTGRES_USER, password=POSTGRES_PASSWORD, dbname=POSTGRES_DB, driver=None
    ).with_bind_ports(5432, port)
    # The session's Postgres runs on the same Docker, so its host reaches this one too.
    settings = copy.deepcopy(test_settings)
    settings.postgres.port = port
    api = create_api(settings)

    def session() -> Generator[Session]:
        with create_session(settings) as session:
            yield session

    api.dependency_overrides[get_sqlalchemy_session] = session
    api.dependency_overrides[get_current_caller] = lambda: TEST_PERSON
    client = TestClient(api, raise_server_exceptions=False)
    try:
        assert client.get('/ready').status_code == 503
        postgres.start()
        assert client.get('/ready').status_code == 200

        # Postgres's image stops on SIGINT, a fast shutdown, as a replaced pod's does.
        postgres.get_wrapped_container().stop()
        ready = client.get('/ready')
        response = client.get(f'{API_PREFIX}/labels')
        assert (ready.status_code, response.status_code, response.json()) == (
            200,
            503,
            {'error': 'Service Unavailable', 'message': 'Postgres is unavailable'},
        )
    finally:
        postgres.stop()
        get_db_engine(settings).dispose()
