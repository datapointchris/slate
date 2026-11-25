"""The 409 for a write that waited behind a fold, and whose record the fold retired before it could land.

A fold locks the idea's `records` row `FOR UPDATE`, and every insert naming the row takes a
key-share lock on it, so such a write waits until the fold commits. The row is gone by then, so its
foreign key check fails. The write resolved the number before the fold began, so no resolver saw
the redirect. Postgres names the missing key in the error's detail, and the redirect it now holds
says where the rows went, so the answer is the one a write through the redirect gets.
"""

from __future__ import annotations

import re

from psycopg.errors import ForeignKeyViolation
from sqlalchemy.exc import DBAPIError

from slate import models
from slate.config import Settings
from slate.database.session import create_session
from slate.services.record_lookup import redirect_of
from slate.services.record_lookup import redirect_sentence

# How Postgres details a key that names no row, as `Key (record_number)=(4086) is not present in table "records".`
MISSING_RECORD = re.compile(r'Key \((?P<columns>[^)]*)\)=\((?P<values>[^)]*)\) is not present in table "records"')


def number_missing_from(error: DBAPIError) -> int | None:
    """The record number a write's foreign key named after its `records` row was deleted, or None for any other error."""
    if not isinstance(error.orig, ForeignKeyViolation):
        return None
    found = MISSING_RECORD.search(error.orig.diag.message_detail or '')
    if found is None:
        return None
    for value in found['values'].split(', '):
        if value.isdecimal():
            return int(value)
    return None


def refusal_behind_a_fold(settings: Settings, error: DBAPIError) -> str | None:
    """The refusal naming where a retired number leads, for a write whose record a fold retired while it waited, else None.

    The write's own transaction is aborted, so the redirect is read in a session of its own.
    """
    number = number_missing_from(error)
    if number is None:
        return None
    with create_session(settings) as session:
        redirect = redirect_of(session, number)
        target = None if redirect is None else session.get(models.Record, redirect.to_number)
        if redirect is None or target is None:
            return None
        return f'{redirect_sentence(session, redirect, target.kind)}; run it against #{redirect.to_number}'
