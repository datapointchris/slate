"""Every setting comes from the environment, so one image runs in any deployment.

A `.env` file in the working directory is read first where one exists, for running the API on a
workstation. A variable already set in the environment outranks it.
"""

import functools
import os

import dotenv
from sqlalchemy.engine import URL


class OIDCSettings:
    """What a bearer token must satisfy to be accepted here.

    `issuer` is the OpenID provider whose signed RFC 9068 access tokens the API trusts. It has no
    default, because a default would trust some other deployment's provider.

    `cli_client_id_prefix` names the clients a person's CLI logs in as (`slate-cli-laptop`). It is
    what keeps another product's token from the same provider out: the device authorization grant
    leaves `aud` empty, so the audience cannot carry that isolation.

    `service_client_id_prefix` names the clients a service authenticates as through the
    client-credentials grant (`slate-svc-worker`). Their tokens reach only the routes their scopes
    list in `slate/api/client_scopes.py`.
    """

    def __init__(self) -> None:
        self.issuer: str = os.environ['OIDC_ISSUER']
        # `or` rather than a get() default: a key present but empty is how a deployment says "use the default".
        self.cli_client_id_prefix: str = os.environ.get('OIDC_CLI_CLIENT_ID_PREFIX') or 'slate-cli-'
        self.service_client_id_prefix: str = os.environ.get('OIDC_SERVICE_CLIENT_ID_PREFIX') or 'slate-svc-'


class FastAPISettings:
    def __init__(self) -> None:
        # The address clients reach this API at, which its RFC 9728 metadata names. Unset, the API
        # serves no metadata. A CLI compares the document's resource with its own URL stripped of a
        # trailing slash, so the slash is stripped here once.
        self.public_url: str | None = (os.environ.get('PUBLIC_URL') or '').rstrip('/') or None
        self.title: str = 'slate'
        self.description: str = 'Work tracker built for coding agents.'


class PostgresSettings:
    def __init__(self) -> None:
        self.host: str = os.environ['POSTGRES_HOST']
        self.username: str = os.environ['POSTGRES_USERNAME']
        self.password: str = os.environ['POSTGRES_PASSWORD']
        self.port: int = int(os.environ.get('POSTGRES_PORT') or 5432)
        self.database: str = os.environ['POSTGRES_DB']
        self.echo: bool = False

    @property
    def db_uri(self) -> str:
        """Each part escaped, so a password holding `@` or `/` cannot be read as the host or the database."""
        url = URL.create(
            'postgresql+psycopg',
            username=self.username,
            password=self.password,
            host=self.host,
            port=self.port,
            database=self.database,
        )
        return url.render_as_string(hide_password=False)


class CheckSettings:
    """When a record checked before comes due again, as `services/check_due.py` orders them.

    The first `head_rows` rows of the queue come due once their last check is `head_days` old, so
    the work about to be taken is read against the world more often. Every other open record comes
    due at `days`.
    """

    def __init__(self) -> None:
        self.head_rows: int = int(os.environ.get('SLATE_CHECK_HEAD_ROWS') or 30)
        self.head_days: int = int(os.environ.get('SLATE_CHECK_HEAD_DAYS') or 7)
        self.days: int = int(os.environ.get('SLATE_CHECK_DAYS') or 30)


class HardToReadSentencesSettings:
    """Where the API sends each title and summary it writes, to be scored for hard-to-read sentences.

    `url` is the hard-to-read-sentences service, which answers `POST /find`. Unset, nothing is sent.
    """

    def __init__(self) -> None:
        self.url: str | None = (os.environ.get('HARD_TO_READ_SENTENCES_URL') or '').rstrip('/') or None


class ReportInboxSettings:
    """Where the API files the `flagged-text` report in a check's answer: an inbox that takes `POST /inbox/reports`.

    The API signs in to it at `OIDC_ISSUER` as `client_id`, with the client-credentials grant. With
    any of the three unset, the API logs each report and files none.
    """

    def __init__(self) -> None:
        self.api_url: str | None = (os.environ.get('FLEET_API_URL') or '').rstrip('/') or None
        self.client_id: str | None = os.environ.get('FLEET_CLIENT_ID') or None
        self.client_secret: str | None = os.environ.get('FLEET_CLIENT_SECRET') or None


class Settings:
    def __init__(self) -> None:
        self.fastapi = FastAPISettings()
        self.oidc = OIDCSettings()
        self.postgres = PostgresSettings()
        self.checks = CheckSettings()
        self.hard_to_read_sentences = HardToReadSentencesSettings()
        self.report_inbox = ReportInboxSettings()


@functools.cache
def get_settings() -> Settings:
    dotenv.load_dotenv(dotenv.find_dotenv(usecwd=True))
    return Settings()
