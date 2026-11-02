import re

import structlog
from sqlalchemy import select
from sqlalchemy import text
from sqlalchemy.orm import Session

from slate import models
from slate.database.base import Base
from slate.models.record_change import RECORDED
from slate.models.record_change import bind_actor

CREDENTIAL = re.compile(r'(^|_)(password|secret|token|api_key)$')
AUDIT_TRIGGERS = text("""
    SELECT CASE WHEN n.nspname = 'public' THEN c.relname ELSE n.nspname || '.' || c.relname END, t.tgargs
    FROM pg_trigger AS t
    JOIN pg_class AS c ON c.oid = t.tgrelid
    JOIN pg_namespace AS n ON n.oid = c.relnamespace
    WHERE t.tgname = 'audit_trail'
""")


def test_a_write_outside_a_request_is_audited_under_its_actor_and_run(txn_api):
    _, session = txn_api
    with structlog.contextvars.bound_contextvars(job_run_id='run-1'):
        job_session = Session(bind=session.connection(), join_transaction_mode='create_savepoint')
        bind_actor(job_session, 'copy-job')
        label = models.IssueLabel(slug='audited', description='written by a job')
        job_session.add(label)
        job_session.flush()
        label_id = str(label.id)
        job_session.delete(label)
        job_session.flush()

    entries = job_session.scalars(
        select(models.RecordChange)
        .where(models.RecordChange.table_name == 'issue_labels', models.RecordChange.record_id == label_id)
        .order_by(models.RecordChange.id)
    ).all()
    assert [entry.operation for entry in entries] == ['insert', 'delete']
    assert {(entry.actor, entry.job_run_id) for entry in entries} == {('copy-job', 'run-1')}
    assert entries[0].changes['slug'] == {'new': 'audited'}
    assert entries[1].changes['slug'] == {'old': 'audited'}


def test_every_table_is_audited_but_what_opts_out(txn_api):
    """A table's trigger comes from its migration, and its opt-outs from the model, so both must agree.

    A missing table needs `CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON <table>
    FOR EACH ROW EXECUTE FUNCTION audit_trail_record('<opted-out column>', ...)` in its migration.
    """
    _, session = txn_api
    triggers = {name: sorted(arg.decode() for arg in bytes(args).split(b'\x00') if arg) for name, args in session.execute(AUDIT_TRIGGERS)}
    audited = [table for table in Base.metadata.sorted_tables if table.info.get(RECORDED, True)]
    expected = {table.fullname: sorted(column.name for column in table.columns if not column.info.get(RECORDED, True)) for table in audited}
    assert triggers == expected

    copied = [
        f'{table.fullname}.{column.name}'
        for table in audited
        for column in table.columns
        if CREDENTIAL.search(column.name) and column.info.get(RECORDED, True)
    ]
    assert copied == [], 'a credential column copied into the audit trail; opt it out'
