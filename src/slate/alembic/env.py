from logging.config import fileConfig

from alembic import context
from sqlalchemy import create_engine
from sqlalchemy import pool

from slate.config import get_settings
from slate.models import RecordChange

alembic_config = context.config

# fileConfig disables every logger that already exists, so a process migrating in-process, such as
# the pytest session, passes configure_logger=False to keep its own.
if alembic_config.config_file_name is not None and alembic_config.attributes.get('configure_logger', True):
    fileConfig(alembic_config.config_file_name)

# Importing slate.models registers every table, and every model shares Base's metadata.
target_metadata = RecordChange.metadata


def database_url() -> str:
    """The URL a caller set on the config, as the pytest session does, else the environment's."""
    return alembic_config.get_main_option('sqlalchemy.url') or get_settings().postgres.db_uri


def run_migrations_offline() -> None:
    context.configure(url=database_url(), target_metadata=target_metadata, literal_binds=True, dialect_opts={'paramstyle': 'named'})
    with context.begin_transaction():
        context.run_migrations()


def run_migrations_online() -> None:
    with create_engine(database_url(), poolclass=pool.NullPool).connect() as connection:
        context.configure(connection=connection, target_metadata=target_metadata)
        with context.begin_transaction():
            context.run_migrations()


if context.is_offline_mode():
    run_migrations_offline()
else:
    run_migrations_online()
