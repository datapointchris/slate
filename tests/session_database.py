"""The session's settings, and the one connection each test's writes share and roll back."""

import copy
import os
from collections.abc import Generator
from collections.abc import Sequence
from copy import deepcopy

from sqlalchemy import Connection
from sqlalchemy.orm import Session

from slate.config import Settings
from tests.test_data import issues

# Settings reads these at construction. `PrivateServices` replaces the Postgres ones once its container is up.
for name, value in {
    'OIDC_ISSUER': 'https://issuer.test',
    'POSTGRES_HOST': 'unset',
    'POSTGRES_USERNAME': 'unset',
    'POSTGRES_PASSWORD': 'unset',
    'POSTGRES_DB': 'unset',
}.items():
    os.environ.setdefault(name, value)

test_settings = Settings()

test_connection: Connection | None = None


def get_test_runner_settings() -> Settings:
    """A copy of the session's settings, which a test may change without reaching any other test."""
    return copy.deepcopy(test_settings)


def set_test_connection(connection: Connection | None) -> None:
    global test_connection
    test_connection = connection


def get_transactional_session() -> Generator[Session]:
    """A session on the test's connection, so the API's writes join the transaction the test rolls back."""
    if test_connection is None:
        raise RuntimeError('No test connection is set; use a txn_api fixture.')
    session = Session(bind=test_connection, join_transaction_mode='create_savepoint', autoflush=False)
    try:
        yield session
    finally:
        session.close()


DATASETS: dict[str, Sequence[object]] = {
    'issues': issues.BASE_DATA,
    'issue_initiatives': issues.INITIATIVES,
    'issue_labels': issues.LABELS,
}


def insert_test_data_transactional(session: Session, *datasets: str) -> None:
    """Add each named dataset to the test's session and flush, so the API reads it inside the same transaction."""
    for name in datasets:
        session.add_all(deepcopy(DATASETS[name]))
    session.flush()
