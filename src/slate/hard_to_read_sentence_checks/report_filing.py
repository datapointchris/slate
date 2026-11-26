"""The `flagged-text` report in a check's answer, filed unchanged to the report inbox under the API's own client.

The report is the whole of one inbox row, so it is sent as the one element of the
`POST /inbox/reports` body. The API signs in with the client-credentials grant, and pyclilogin's
`ServiceTokenSource` keeps the token in memory until it lapses.

A filing is tried three times. A refusal that a retry cannot change, such as a 400 or a 403, ends
it at once. Each filing that is never kept is logged at error with the whole report, so it can
still be filed by hand.
"""

import dataclasses as dc
import time
from collections.abc import Callable
from typing import Any

import httpx2
import structlog
from pyclilogin import LoginError
from pyclilogin import Provider
from pyclilogin import ServiceRefused
from pyclilogin import ServiceTokenSource

from slate.config import ReportInboxSettings

logger = structlog.get_logger()

INBOX_RECORD_SCOPE = 'fleet.inbox.record'
ATTEMPTS = 3
# The pause before each retry, in seconds.
RETRY_PAUSES = (1.0, 2.0)
FILING_WAIT_SECONDS = 10
REPAIR = 'Check FLEET_CLIENT_ID and FLEET_CLIENT_SECRET.'
# Besides a 5xx, the statuses where the inbox timed out or was busy, so a later attempt may be kept.
RETRIED_STATUSES = frozenset({408, 429})
KEPT_STATUSES = frozenset({200, 201})


@dc.dataclass(frozen=True)
class FailedFiling:
    why: str
    retried: bool


class ReportFiler:
    """Files each report at `api_url` as the client `tokens` signs in as. With either unset, it logs the report instead."""

    def __init__(
        self,
        api_url: str | None,
        tokens: ServiceTokenSource | None,
        http: httpx2.Client | None = None,
        pause: Callable[[float], None] = time.sleep,
    ) -> None:
        self.api_url = api_url
        self.tokens = tokens
        self.http = http or httpx2.Client(timeout=FILING_WAIT_SECONDS, follow_redirects=False)
        self.pause = pause

    @classmethod
    def from_settings(cls, inbox: ReportInboxSettings, issuer: str) -> ReportFiler:
        if not (inbox.client_id and inbox.client_secret):
            return cls(inbox.api_url, None)
        provider = Provider(issuer, inbox.client_id, scopes=(INBOX_RECORD_SCOPE,))
        return cls(inbox.api_url, ServiceTokenSource(provider, inbox.client_secret, repair=REPAIR))

    def file(self, report: dict[str, Any]) -> None:
        key = report.get('key')
        if self.api_url is None or self.tokens is None:
            why = 'FLEET_API_URL, FLEET_CLIENT_ID or FLEET_CLIENT_SECRET is unset'
            logger.warning('flagged_text_report_not_filed', key=key, why=why, report=report)
            return
        attempt = 0
        failed: FailedFiling | None = None
        for attempt in range(1, ATTEMPTS + 1):
            failed = self.attempt(report)
            if failed is None:
                logger.info('flagged_text_report_filed', key=key, attempt=attempt)
                return
            if not failed.retried or attempt == ATTEMPTS:
                break
            logger.warning('flagged_text_report_retried', key=key, attempt=attempt, why=failed.why)
            self.pause(RETRY_PAUSES[attempt - 1])
        logger.error('flagged_text_report_not_filed', key=key, attempts=attempt, why=failed and failed.why, report=report)

    def attempt(self, report: dict[str, Any]) -> FailedFiling | None:
        assert self.tokens is not None
        try:
            token = self.tokens.token()
        except ServiceRefused as error:
            return FailedFiling(str(error), retried=False)
        except LoginError as error:
            return FailedFiling(f'no token for {self.tokens.provider.client_id}: {error}', retried=True)
        url = f'{self.api_url}/inbox/reports'
        try:
            response = self.http.post(url, json=[report], headers={'Authorization': f'Bearer {token.access_token}'})
        except httpx2.HTTPError as error:
            return FailedFiling(f'the inbox at {url} did not answer: {error!r}', retried=True)
        if response.status_code in KEPT_STATUSES:
            return None
        retried = response.status_code >= 500 or response.status_code in RETRIED_STATUSES
        return FailedFiling(f'the inbox at {url} answered {response.status_code}: {response.text[:500]}', retried=retried)
