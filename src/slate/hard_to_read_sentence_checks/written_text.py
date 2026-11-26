"""Each title and summary a write adds or changes, sent to be checked once its transaction commits.

A request that writes attaches a `WrittenTextWatch` to its session. Each flush notes every issue,
idea and initiative whose checked text it added or changed, so every write path is covered with no
call of its own: a create, an edit, a promotion. Each record's latest text goes once the
transaction commits, as one check naming the record's number, and nothing goes when it ends
without committing.
"""

import dataclasses as dc
from typing import Any

from sqlalchemy import event
from sqlalchemy import inspect
from sqlalchemy.orm import Session
from sqlalchemy.orm import SessionTransaction
from sqlalchemy.orm import UOWTransaction

from slate import models
from slate.hard_to_read_sentence_checks.sender import DOOR
from slate.hard_to_read_sentence_checks.sender import CheckSender
from slate.models.record_change import FLOW

WATCH = 'hard_to_read_sentence_checks_watch'

# Each kind's checked columns, and the request field each fills. An initiative's name is its title.
CHECKED_TEXT: dict[type, dict[str, str]] = {
    models.Issue: {'title': 'slate.title'},
    models.Idea: {'title': 'slate.title'},
    models.Initiative: {'name': 'slate.title', 'summary': 'slate.summary'},
}


@dc.dataclass
class WrittenTextWatch:
    """The sender, the labels the request claims, and each record's written fields by its number."""

    sender: CheckSender
    labels: dict[str, str]
    written: dict[int, dict[str, str]] = dc.field(default_factory=dict)


def watch_written_text(session: Session, sender: CheckSender, labels: dict[str, str]) -> None:
    session.info[WATCH] = WrittenTextWatch(sender, labels)


def written_text(record: Any) -> dict[str, str]:
    """Each checked field whose text the pending flush adds or changes. Text set to what it already held is left out."""
    columns = CHECKED_TEXT.get(type(record), {})
    state = inspect(record)
    fields = {}
    for column, field in columns.items():
        history = state.attrs[column].history
        if not history.added:
            continue
        text = history.added[-1]
        if text and not (history.deleted and history.deleted[-1] == text):
            fields[field] = text
    return fields


@event.listens_for(Session, 'after_flush')
def note_written_text(session: Session, _flush_context: UOWTransaction) -> None:
    """The flush has numbered each new record, and each one's history still holds what it changed."""
    watch: WrittenTextWatch | None = session.info.get(WATCH)
    if watch is None:
        return
    for record in (*session.new, *session.dirty):
        if fields := written_text(record):
            watch.written.setdefault(record.number, {}).update(fields)


@event.listens_for(Session, 'after_commit')
def send_written_text(session: Session) -> None:
    watch: WrittenTextWatch | None = session.info.get(WATCH)
    if watch is None or not watch.written:
        return
    written, watch.written = watch.written, {}
    flow = session.info.get(FLOW)
    labels = watch.labels | ({'flow_id': flow} if isinstance(flow, str) and flow else {})
    for number, fields in written.items():
        watch.sender.send({'door': DOOR, 'fields': fields, 'record': str(number), **labels})


@event.listens_for(Session, 'after_transaction_end')
def drop_unsent_text(session: Session, transaction: SessionTransaction) -> None:
    """A transaction that ends without its commit wrote nothing, so its text is never sent."""
    watch: WrittenTextWatch | None = session.info.get(WATCH)
    if watch is not None and transaction.parent is None:
        watch.written.clear()
