"""Every writing request's titles and summaries go to the hard-to-read-sentences service once it commits.

`check_sender` builds the sender from the settings, and none where `HARD_TO_READ_SENTENCES_URL` is
unset. `watch_text_written` runs on every route, and labels a write's checks with its caller's
client id and the agent session its baggage names.
"""

from fastapi import Request
from opentelemetry import baggage
from opentelemetry.sdk._logs import LoggerProvider

from slate.api.callers import SAFE_METHODS
from slate.api.callers import CurrentCaller
from slate.api.callers import DbSession
from slate.config import Settings
from slate.hard_to_read_sentence_checks.report_filing import ReportFiler
from slate.hard_to_read_sentence_checks.sender import CheckSender
from slate.hard_to_read_sentence_checks.written_text import watch_written_text
from slate.tracing.writer_flow import SESSION_ID_MEMBER


def check_sender(settings: Settings, logger_provider: LoggerProvider | None) -> CheckSender | None:
    if settings.hard_to_read_sentences.url is None:
        return None
    filer = ReportFiler.from_settings(settings.report_inbox, settings.oidc.issuer)
    return CheckSender(settings.hard_to_read_sentences.url, filer, logger_provider)


def watch_text_written(request: Request, session: DbSession, caller: CurrentCaller) -> None:
    sender: CheckSender | None = request.app.state.hard_to_read_sentence_checks
    if sender is None or request.method in SAFE_METHODS:
        return
    labels = {'client_id': caller.client_id}
    session_id = baggage.get_baggage(SESSION_ID_MEMBER)
    if isinstance(session_id, str) and session_id:
        labels['session_id'] = session_id
    watch_written_text(session, sender, labels)
