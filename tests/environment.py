"""The Postgres a pytest session runs against.

Every session starts its own on a port Docker picks, so two checkouts run pytest at once and neither
can truncate the other's tables. testcontainers' reaper removes it when the session ends, however
it ends.
"""

import logging
from pathlib import Path

import pytest
import yaml
from alembic import command
from alembic.config import Config
from testcontainers.community.postgres import PostgresContainer
from testcontainers.core.config import testcontainers_config

from slate.config import Settings

logger = logging.getLogger(__name__)

# Readiness is checked every 100ms instead of every second, under the same two-minute ceiling.
testcontainers_config.sleep_time = 0.1
testcontainers_config.max_tries = 1200

REPO_ROOT = Path(__file__).resolve().parents[1]
POSTGRES_USER = 'postgres'
POSTGRES_PASSWORD = 'postgres'
POSTGRES_DB = 'slate'
POSTGRES_DATA = '/var/lib/postgresql/data'


def compose_postgres_image() -> str:
    """The image the compose stack runs Postgres on, so the tests meet the same major version."""
    compose = yaml.safe_load((REPO_ROOT / 'docker-compose.yml').read_text())
    return compose['services']['postgres']['image']


def migrate_to_head(settings: Settings) -> None:
    config = Config(str(REPO_ROOT / 'alembic.ini'))
    # The option is read through ConfigParser interpolation, so the URL's percent-escapes are doubled.
    config.set_main_option('sqlalchemy.url', settings.postgres.db_uri.replace('%', '%%'))
    config.attributes['configure_logger'] = False
    command.upgrade(config, 'head')


class PrivateServices:
    """Starts this session's Postgres, points `settings` at it and migrates it to head."""

    def __init__(self, settings: Settings):
        self.settings = settings
        # Durability is off and the data lives in memory: nothing outlives the session.
        self.postgres = (
            PostgresContainer(compose_postgres_image(), username=POSTGRES_USER, password=POSTGRES_PASSWORD, dbname=POSTGRES_DB, driver=None)
            .with_tmpfs_mount(POSTGRES_DATA)
            .with_command(['postgres', '-c', 'fsync=off', '-c', 'synchronous_commit=off', '-c', 'full_page_writes=off'])
        )
        self.started = False

    def __enter__(self):
        try:
            self.postgres.start()
            self.started = True
            postgres = self.settings.postgres
            postgres.host = self.postgres.get_container_host_ip()
            postgres.port = int(self.postgres.get_exposed_port(5432))
            postgres.username = POSTGRES_USER
            postgres.password = POSTGRES_PASSWORD
            postgres.database = POSTGRES_DB
            logger.info(f'session postgres: {postgres.host}:{postgres.port}')
            migrate_to_head(self.settings)
        except Exception as e:
            self.stop()
            pytest.exit(f'Could not start the Postgres this session runs against: {e}', returncode=1)
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.stop()
        return False

    def stop(self) -> None:
        if self.started:
            self.postgres.stop()
            self.started = False
