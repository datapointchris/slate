import logging
import time
from logging.config import fileConfig

from alembic import context
from sqlalchemy import Connection
from sqlalchemy import Engine
from sqlalchemy import create_engine
from sqlalchemy import pool
from sqlalchemy.exc import OperationalError

from slate.config import get_settings
from slate.models import RecordChange

alembic_config = context.config

# fileConfig disables every logger that already exists, so a process migrating in-process, such as
# the pytest session, passes configure_logger=False to keep its own.
if alembic_config.config_file_name is not None and alembic_config.attributes.get('configure_logger', True):
    fileConfig(alembic_config.config_file_name)

# Importing slate.models registers every table, and every model shares Base's metadata.
target_metadata = RecordChange.metadata

logger = logging.getLogger('alembic.env')

# A network policy may admit a new pod's traffic only after it starts, refusing its first connections.
CONNECT_DEADLINE_SECONDS = 60
CONNECT_RETRY_SECONDS = 1


def database_url() -> str:
    """The URL a caller set on the config, as the pytest session does, else the environment's."""
    return alembic_config.get_main_option('sqlalchemy.url') or get_settings().postgres.db_uri


def run_migrations_offline() -> None:
    context.configure(url=database_url(), target_metadata=target_metadata, literal_binds=True, dialect_opts={'paramstyle': 'named'})
    with context.begin_transaction():
        context.run_migrations()


def first_connection(engine: Engine) -> Connection:
    deadline = time.monotonic() + CONNECT_DEADLINE_SECONDS
    while True:
        try:
            return engine.connect()
        except OperationalError as error:
            if time.monotonic() >= deadline:
                raise
            logger.warning('database refused the connection, retrying in %ss: %s', CONNECT_RETRY_SECONDS, error.orig)
            time.sleep(CONNECT_RETRY_SECONDS)


def run_migrations_online() -> None:
    with first_connection(create_engine(database_url(), poolclass=pool.NullPool)) as connection:
        context.configure(connection=connection, target_metadata=target_metadata)
        with context.begin_transaction():
            context.run_migrations()


if context.is_offline_mode():
    run_migrations_offline()
else:
    run_migrations_online()
