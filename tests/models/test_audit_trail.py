import structlog
from sqlalchemy import select
from sqlalchemy.orm import Session

from slate import models
from slate.models.record_change import bind_actor


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
