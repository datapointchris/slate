"""Which database errors mean Postgres stopped serving, rather than refused what it was asked."""

import re

import psycopg
from psycopg import errors as postgres_errors
from sqlalchemy.exc import DBAPIError

# A session the server cut off for a shutdown, or because another backend crashed, and a
# connection it refused while starting up, shutting down or recovering.
SERVER_STOPPING = (postgres_errors.AdminShutdown, postgres_errors.CrashShutdown, postgres_errors.CannotConnectNow)

# libpq keeps only the text of an error the server sends at login, with no SQLSTATE, as
# `FATAL:  <message>`. A refusal while the server starts, stops or recovers opens with
# `the database system is`. Any other is the login itself refused, such as a wrong password.
SERVER_FATAL_MESSAGE = re.compile(r'FATAL:\s+(.*)')
SERVER_STOPPING_MESSAGE = 'the database system is '


def login_refused(driver_error: psycopg.Error) -> bool:
    return any(not message.startswith(SERVER_STOPPING_MESSAGE) for message in SERVER_FATAL_MESSAGE.findall(str(driver_error)))


def postgres_unavailable(error: BaseException) -> bool:
    """Whether `error` is a connection refused, dropped or cut off by Postgres shutting down."""
    if not isinstance(error, DBAPIError):
        return False
    # SQLAlchemy's dialect found the connection closed or broken under the statement.
    if error.connection_invalidated:
        return True
    driver_error = error.orig
    if isinstance(driver_error, SERVER_STOPPING):
        return True
    # Every error the server answers carries a SQLSTATE, so one without is the driver not reaching it.
    return isinstance(driver_error, psycopg.OperationalError) and driver_error.sqlstate is None and not login_refused(driver_error)
