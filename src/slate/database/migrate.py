"""What the migrate Job runs: `alembic upgrade head`, unless a later release already migrated the database."""

import logging
import time
from logging.config import fileConfig

from alembic import command
from alembic.config import Config
from alembic.runtime.migration import MigrationContext
from alembic.script import ScriptDirectory
from alembic.util import CommandError
from sqlalchemy import Connection
from sqlalchemy import Engine
from sqlalchemy import create_engine
from sqlalchemy import pool
from sqlalchemy.exc import OperationalError

from slate.config import get_settings

# Named rather than __name__, which reads __main__ under `python -m`.
logger = logging.getLogger('slate.database.migrate')

# A network policy may admit a new pod's traffic only after it starts, refusing its first connections.
CONNECT_DEADLINE_SECONDS = 60
CONNECT_RETRY_SECONDS = 1


def database_url(config: Config) -> str:
    """The URL a caller set on the config, as the pytest session does, else the environment's."""
    return config.get_main_option('sqlalchemy.url') or get_settings().postgres.db_uri


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


def revisions_from_a_later_release(config: Config) -> list[str]:
    """The database's revisions that none of this release's migrations hold."""
    script = ScriptDirectory.from_config(config)
    with first_connection(create_engine(database_url(config), poolclass=pool.NullPool)) as connection:
        current = MigrationContext.configure(connection).get_current_heads()
    unknown = []
    for revision in current:
        try:
            script.get_revision(revision)
        except CommandError:
            unknown.append(revision)
    return unknown


def upgrade_to_head(config: Config) -> None:
    # A rollback runs an earlier release's Job on a later one's schema, which each migration keeps it able to serve on.
    if later := revisions_from_a_later_release(config):
        heads = ', '.join(ScriptDirectory.from_config(config).get_heads())
        logger.warning("the database is at %s, past this release's head %s, so nothing is migrated", ', '.join(later), heads)
        return
    command.upgrade(config, 'head')


def main() -> None:
    config = Config('alembic.ini')
    if config.config_file_name is not None:
        fileConfig(config.config_file_name, disable_existing_loggers=False)
    # The logging is configured once, here, so env.py leaves it alone.
    config.attributes['configure_logger'] = False
    upgrade_to_head(config)


if __name__ == '__main__':
    main()
