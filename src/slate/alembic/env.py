from logging.config import fileConfig

from alembic import context
from sqlalchemy import create_engine
from sqlalchemy import pool

from slate.database.migrate import database_url
from slate.database.migrate import first_connection
from slate.models import RecordChange

alembic_config = context.config

# fileConfig replaces the root logger's handlers, so a process migrating in-process, such as the
# pytest session, passes configure_logger=False to keep its own. Loggers created before it, such
# as slate.database.migrate's, keep logging.
if alembic_config.config_file_name is not None and alembic_config.attributes.get('configure_logger', True):
    fileConfig(alembic_config.config_file_name, disable_existing_loggers=False)

# Importing slate.models registers every table, and every model shares Base's metadata.
target_metadata = RecordChange.metadata


def run_migrations_offline() -> None:
    context.configure(
        url=database_url(alembic_config), target_metadata=target_metadata, literal_binds=True, dialect_opts={'paramstyle': 'named'}
    )
    with context.begin_transaction():
        context.run_migrations()


def run_migrations_online() -> None:
    with first_connection(create_engine(database_url(alembic_config), poolclass=pool.NullPool)) as connection:
        context.configure(connection=connection, target_metadata=target_metadata)
        with context.begin_transaction():
            context.run_migrations()


if context.is_offline_mode():
    run_migrations_offline()
else:
    run_migrations_online()
