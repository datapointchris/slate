"""A person's pin: a record held ahead of every priority in the queue until someone unpins or closes it.

The pin is the one way work crosses a priority, so no scope reaches its routes and an automated
order can never pin its own favorite. A token is its person's whichever session holds it, so the
pin records the agent session where one sent it.
"""

from __future__ import annotations

import datetime as dt

from sqlalchemy.orm import Session

from slate import models
from slate import schemas
from slate.services.worklogs import record_session
from slate.services.write_effects import WriteEffectCode
from slate.services.write_effects import WriteEffects


def pin_record(session: Session, record: models.Record, request: schemas.RecordPin, signed_in: str, effects: WriteEffects) -> None:
    """Stamp the pin. A record already pinned keeps the pin it has, and the answer warns so."""
    if record.pinned_ts is not None:
        effects.warn(WriteEffectCode.ALREADY_PINNED, f'#{record.number} was already pinned by {record.pinned_by}, so its pin stands.')
        return
    if request.session is not None:
        record_session(session, request.session, request.host)
    record.pinned_ts = dt.datetime.now(dt.UTC)
    record.pinned_by = signed_in
    record.pinned_session = request.session


def unpin_record(record: models.Record, effects: WriteEffects) -> None:
    if record.pinned_ts is None:
        effects.warn(WriteEffectCode.NOT_PINNED, f'#{record.number} is not pinned, so nothing changed.')
        return
    clear_pin(record)


def clear_pin(record: models.Record) -> None:
    record.pinned_ts = None
    record.pinned_by = None
    record.pinned_session = None


def clear_pin_on_close(closing: models.Issue | models.Initiative, became: str, effects: WriteEffects) -> None:
    """A closed or shelved record holds no place in the queue, so its pin ends and the answer says so."""
    if closing.pinned_ts is None:
        return
    effects.did(WriteEffectCode.PIN_CLEARED, f'#{closing.number} lost the pin {closing.pinned_by} set, because it became {became}.')
    clear_pin(closing.record)


def carry_pin(idea: models.Idea, initiative: models.Initiative, effects: WriteEffects) -> None:
    """A promoted idea's work is its initiative's now, so the pin goes with it."""
    if idea.pinned_ts is None:
        return
    source, target = idea.record, initiative.record
    target.pinned_ts, target.pinned_by, target.pinned_session = source.pinned_ts, source.pinned_by, source.pinned_session
    clear_pin(source)
    effects.did(WriteEffectCode.PIN_CARRIED, f'The pin on #{idea.number} moved to #{initiative.number}.')
