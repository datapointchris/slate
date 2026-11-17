from collections.abc import Iterator

import pytest
from alembic.config import Config
from sqlalchemy import Engine
from sqlalchemy import create_engine
from sqlalchemy import make_url
from sqlalchemy import pool
from sqlalchemy import text

from tests.environment import alembic_config

SCRATCH_DATABASE = 'slate_scratch'


@pytest.fixture
def scratch_database(private_services) -> Iterator[tuple[Config, Engine]]:
    """An empty database beside the session's, migrated by the superuser in a test of its own, dropped when it ends."""
    superuser_uri = private_services.superuser_uri()
    admin = create_engine(superuser_uri, isolation_level='AUTOCOMMIT', poolclass=pool.NullPool)
    with admin.connect() as connection:
        connection.execute(text(f'CREATE DATABASE {SCRATCH_DATABASE}'))
    url = make_url(superuser_uri).set(database=SCRATCH_DATABASE).render_as_string(hide_password=False)
    engine = create_engine(url, poolclass=pool.NullPool)
    try:
        yield alembic_config(url), engine
    finally:
        engine.dispose()
        with admin.connect() as connection:
            connection.execute(text(f'DROP DATABASE {SCRATCH_DATABASE} WITH (FORCE)'))
        admin.dispose()
