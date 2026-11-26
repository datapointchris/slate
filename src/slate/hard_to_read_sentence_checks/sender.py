"""Each record's written fields, posted to the hard-to-read-sentences service's `POST /find` by worker threads.

A write only queues its check, so a hung service holds no request thread and no database session.
The request and the answer follow the service's published `find-request` and `find-answer` JSON
Schemas. Each check waits `FIND_WAIT_SECONDS` for its answer.

When a check gets no answer, a non-200 status or a body missing what the answer schema requires,
the sender emits one OTLP log record per field, with `result: unanswered`. The service emits the
record of every check it answers, so the sender writes no other. Each attribute is the name that
the request schema gives the label as its `x-attribute`, and the Body holds the flagged sentences,
`[]` here.
"""

import contextlib
import dataclasses as dc
import datetime as dt
import queue
import threading
import time
from typing import Any

import httpx2
import structlog
from opentelemetry import context as otel_context
from opentelemetry._logs import LoggerProvider
from opentelemetry._logs import SeverityNumber
from opentelemetry._logs import get_logger_provider

from slate.hard_to_read_sentence_checks.report_filing import ReportFiler

logger = structlog.get_logger()

DOOR = 'slate-api'
FIND_WAIT_SECONDS = 5
# Titles and summaries arrive a few a minute, so this holds a long burst while the service is away.
QUEUE_SIZE = 1000
WORKERS = 2

KIND = 'hard_to_read_sentences.check'
SCHEMA_VERSION = '1'
PREFIX = 'fleet.hard_to_read_sentences.check'
# Each label slate sends, and the record attribute its request schema names.
LABEL_ATTRIBUTES = {
    'record': f'{PREFIX}.record',
    'client_id': f'{PREFIX}.client_id',
    'session_id': 'session.id',
    'flow_id': 'fleet.flow.id',
}
ANSWER_REQUIRES = ('sentences', 'checked', 'flagged', 'rules', 'refusal')
ACTIONS = ('deny', 'warn')


@dc.dataclass(frozen=True)
class QueuedCheck:
    """A `POST /find` body, and the context of the write that queued it, so its records carry the write's trace."""

    request: dict[str, Any]
    context: otel_context.Context


STOP = QueuedCheck({}, otel_context.Context())


class CheckSender:
    """Sends each queued check from `WORKERS` threads, which start with the first check."""

    def __init__(
        self, url: str, filer: ReportFiler, logger_provider: LoggerProvider | None = None, http: httpx2.Client | None = None
    ) -> None:
        self.url = url
        self.filer = filer
        self.records = (logger_provider or get_logger_provider()).get_logger(__name__)
        self.http = http or httpx2.Client(timeout=FIND_WAIT_SECONDS, follow_redirects=False)
        self.queue: queue.Queue[QueuedCheck] = queue.Queue(QUEUE_SIZE)
        self.workers: list[threading.Thread] = []
        self.starting = threading.Lock()

    def send(self, request: dict[str, Any]) -> None:
        """Queue one record's fields to be checked. A full queue records them unanswered at once."""
        self.start()
        check = QueuedCheck(request, otel_context.get_current())
        try:
            self.queue.put_nowait(check)
        except queue.Full:
            self.record_unanswered(check, f'the API already held {QUEUE_SIZE} checks waiting to be sent')

    def start(self) -> None:
        with self.starting:
            if self.workers:
                return
            self.workers = [threading.Thread(target=self.work, name=f'sentence-check-{n}', daemon=True) for n in range(WORKERS)]
            for worker in self.workers:
                worker.start()

    def stop(self, wait_seconds: float) -> None:
        """Send what is queued within `wait_seconds`, then record each check still waiting as unanswered."""
        deadline = time.monotonic() + wait_seconds
        for _ in self.workers:
            with contextlib.suppress(queue.Full):
                self.queue.put_nowait(STOP)
        for worker in self.workers:
            worker.join(max(0.0, deadline - time.monotonic()))
        while True:
            try:
                check = self.queue.get_nowait()
            except queue.Empty:
                return
            if check is not STOP:
                self.record_unanswered(check, 'the API stopped before it sent the check')

    def work(self) -> None:
        while (check := self.queue.get()) is not STOP:
            try:
                self.check(check)
            except Exception as error:
                logger.exception('sentence_check_failed', record=check.request.get('record'))
                self.record_unanswered(check, f'the API failed while it sent the check: {error!r}')

    def check(self, check: QueuedCheck) -> None:
        request = check.request
        try:
            response = self.http.post(f'{self.url}/find', json=request)
        except httpx2.HTTPError as error:
            self.record_unanswered(check, f'the service at {self.url} did not answer: {error!r}')
            return
        if response.status_code != 200:
            self.record_unanswered(check, f'the service answered {response.status_code}')
            return
        try:
            answer = response.json()
        except ValueError:
            self.record_unanswered(check, 'the service answered something other than JSON')
            return
        if not answered_whole(answer):
            self.record_unanswered(check, 'the service answered without an action or a key the answer schema requires')
            return
        logger.info(
            'sentence_check_answered',
            record=request.get('record'),
            fields=sorted(request['fields']),
            action=answer['action'],
            flagged=len(answer['flagged']),
        )
        if (report := answer.get('flagged_text_report')) is not None:
            self.filer.file(report)

    def record_unanswered(self, check: QueuedCheck, why: str) -> None:
        request = check.request
        checked = dt.datetime.now(dt.UTC)
        logger.warning('sentence_check_unanswered', record=request.get('record'), fields=sorted(request['fields']), why=why)
        for field in request['fields']:
            self.records.emit(
                timestamp=time.time_ns(),
                context=check.context,
                severity_number=SeverityNumber.WARN,
                severity_text='WARN',
                body='[]',
                attributes=unanswered_attributes(request, field, why, checked),
            )


def answered_whole(answer: object) -> bool:
    return (
        isinstance(answer, dict)
        and answer.get('action') in ACTIONS
        and isinstance(answer.get('flagged'), list)
        and all(key in answer for key in ANSWER_REQUIRES)
    )


def unanswered_attributes(request: dict[str, Any], field: str, why: str, checked: dt.datetime) -> dict[str, str]:
    """One field's unanswered check, with the request schema's defaults for the labels slate never sends."""
    found = {
        'fleet.kind': KIND,
        'fleet.schema_version': SCHEMA_VERSION,
        f'{PREFIX}.ts': checked.isoformat(timespec='milliseconds').replace('+00:00', 'Z'),
        f'{PREFIX}.door': request['door'],
        f'{PREFIX}.field': field,
        f'{PREFIX}.result': 'unanswered',
        f'{PREFIX}.action': '',
        f'{PREFIX}.flag': '',
        f'{PREFIX}.why_unchecked': why,
        f'{PREFIX}.replayed': 'false',
        f'{PREFIX}.round': '0',
    }
    for label, attribute in LABEL_ATTRIBUTES.items():
        if value := request.get(label):
            found[attribute] = str(value)
    return found
