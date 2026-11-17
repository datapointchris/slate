"""The Postgres a pytest session runs against.

Every session starts its own on a port Docker picks, so two checkouts run pytest at once and neither
can truncate the other's tables. testcontainers' reaper removes it when the session ends, however
it ends.

It holds the two roles the cluster's Postgres gives slate: `slate`, which owns every object and runs
the migrations, and `slate_app`, which the API logs in as with DML and no DDL. The tests run as
`slate_app`, so a grant the cluster would not give fails here first.
"""

import logging
from pathlib import Path

import psycopg
import pytest
import yaml
from alembic import command
from alembic.config import Config
from psycopg import sql
from sqlalchemy import URL
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
OWNER = 'slate'
OWNER_PASSWORD = 'slate-owner'
APP = 'slate_app'
APP_PASSWORD = 'slate-app'

# What homelab's reconcile-databases.sh gives a declared database's two roles, on an empty database.
CREATE_ROLES = (
    'CREATE ROLE {owner} LOGIN PASSWORD {owner_password}',
    'CREATE ROLE {app} LOGIN PASSWORD {app_password}',
    'ALTER DATABASE {database} OWNER TO {owner}',
    'REVOKE ALL ON DATABASE {database} FROM PUBLIC',
    'GRANT CONNECT ON DATABASE {database} TO {app}',
    'GRANT USAGE ON SCHEMA public TO {app}',
    'REVOKE CREATE ON SCHEMA public FROM PUBLIC, {app}',
    'ALTER DEFAULT PRIVILEGES FOR ROLE {owner} GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO {app}',
    'ALTER DEFAULT PRIVILEGES FOR ROLE {owner} GRANT USAGE ON SEQUENCES TO {app}',
    'ALTER DEFAULT PRIVILEGES FOR ROLE {owner} GRANT USAGE ON SCHEMAS TO {app}',
)


def compose_postgres_image() -> str:
    """The image the compose stack runs Postgres on, so the tests meet the same major version."""
    compose = yaml.safe_load((REPO_ROOT / 'docker-compose.yml').read_text())
    return compose['services']['postgres']['image']


def alembic_config(db_uri: str) -> Config:
    config = Config(str(REPO_ROOT / 'alembic.ini'))
    # The option is read through ConfigParser interpolation, so the URL's percent-escapes are doubled.
    config.set_main_option('sqlalchemy.url', db_uri.replace('%', '%%'))
    config.attributes['configure_logger'] = False
    return config


def migrate_to_head(settings: Settings) -> None:
    command.upgrade(alembic_config(settings.postgres.db_uri), 'head')


def create_roles(host: str, port: int) -> None:
    """Create the owner and app roles as the container's superuser, before any table exists."""
    names = {
        'owner': sql.Identifier(OWNER),
        'app': sql.Identifier(APP),
        'database': sql.Identifier(POSTGRES_DB),
        'owner_password': sql.Literal(OWNER_PASSWORD),
        'app_password': sql.Literal(APP_PASSWORD),
    }
    with psycopg.connect(
        host=host, port=port, user=POSTGRES_USER, password=POSTGRES_PASSWORD, dbname=POSTGRES_DB, autocommit=True
    ) as connection:
        for statement in CREATE_ROLES:
            connection.execute(sql.SQL(statement).format(**names))


class PrivateServices:
    """Starts this session's Postgres, migrates it to head as the owner and points `settings` at it as the app."""

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
            postgres.database = POSTGRES_DB
            logger.info(f'session postgres: {postgres.host}:{postgres.port}')
            create_roles(postgres.host, postgres.port)
            postgres.username, postgres.password = OWNER, OWNER_PASSWORD
            migrate_to_head(self.settings)
            postgres.username, postgres.password = APP, APP_PASSWORD
        except Exception as e:
            self.stop()
            pytest.exit(f'Could not start the Postgres this session runs against: {e}', returncode=1)
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.stop()
        return False

    def run_as_owner(self, statement: str) -> None:
        """Run one statement as the owner, committed."""
        postgres = self.settings.postgres
        with psycopg.connect(
            host=postgres.host, port=postgres.port, user=OWNER, password=OWNER_PASSWORD, dbname=POSTGRES_DB, autocommit=True
        ) as connection:
            connection.execute(statement)

    def superuser_uri(self) -> str:
        """The container's superuser, which alone may create and drop a database."""
        postgres = self.settings.postgres
        url = URL.create('postgresql+psycopg', POSTGRES_USER, POSTGRES_PASSWORD, postgres.host, postgres.port, POSTGRES_DB)
        return url.render_as_string(hide_password=False)

    def stop(self) -> None:
        if self.started:
            self.postgres.stop()
            self.started = False
