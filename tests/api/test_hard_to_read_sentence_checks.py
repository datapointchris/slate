"""A script's title is written whatever the sentence service answers, then scored, and a flagged one's report is filed."""

import copy
import dataclasses as dc
import gzip
import json
import threading
from collections.abc import Callable
from collections.abc import Iterator
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler
from http.server import ThreadingHTTPServer
from typing import Any
from urllib.parse import parse_qs

from fastapi import status
from opentelemetry.exporter.otlp.proto.http._log_exporter import OTLPLogExporter
from opentelemetry.proto.collector.logs.v1.logs_service_pb2 import ExportLogsServiceRequest

from slate.api.main import create_api
from slate.api.oidc_auth import ScopedClient
from slate.logger import new_logger_provider
from tests.conftest import transactional_api_client
from tests.session_database import test_settings
from tests.util import show_status_and_response

SCRIPT = ScopedClient(client_id='slate-svc-nightly-import', scopes=frozenset({'slate.records.plan'}))
FLAGGED_TITLE = 'An import that a script runs files each row it reads as an issue whose title the row names.'
INBOX_TOKEN = 'inbox-access-token'
CHECK = 'fleet.hard_to_read_sentences.check'


@dc.dataclass(frozen=True)
class Received:
    path: str
    headers: dict[str, str]
    body: bytes


# A route's answer to what it received: a status and a JSON body, or raw bytes.
Route = Callable[[Received], tuple[int, Any]]


@contextmanager
def serving(routes: dict[tuple[str, str], Route]) -> Iterator[tuple[str, list[Received]]]:
    """A server on a free local port that answers each (method, path) from `routes`, and every request it received."""
    received: list[Received] = []

    class Handler(BaseHTTPRequestHandler):
        def answer(self, method: str) -> None:
            body = self.rfile.read(int(self.headers.get('Content-Length') or 0))
            request = Received(self.path, {k.lower(): v for k, v in self.headers.items()}, body)
            received.append(request)
            route = routes.get((method, self.path))
            code, content = route(request) if route else (404, {'error': 'no route'})
            data = content if isinstance(content, bytes) else json.dumps(content).encode()
            self.send_response(code)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def do_GET(self) -> None:
            self.answer('GET')

        def do_POST(self) -> None:
            self.answer('POST')

        def log_message(self, *_args: object) -> None:
            pass

    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        yield f'http://127.0.0.1:{server.server_port}', received
    finally:
        server.shutdown()
        server.server_close()


def flagged_answer(request: dict[str, Any]) -> dict[str, Any]:
    """What the service answers a warned door for a flagged title, built from the request as the service builds it."""
    record, title = request['record'], request['fields']['slate.title']
    sentence = {'sentence': title, 'score': 4, 'signs': ['opens_on_a', 'reduced_clauses']}
    refusal = f'1. "{title}"\n(opens_on_a, reduced_clauses)'
    return {
        'door': 'slate-api',
        'action': 'warn',
        'flag': 'deny',
        'sentences': 1,
        'checked': {'slate.title': {'sentences': 1, 'flagged': 1}},
        'flagged': [{'field': 'slate.title', **sentence}],
        'rules': [{'sign': 'opens_on_a', 'rule': 'Open on a known actor.', 'example': None}],
        'refusal': refusal,
        'flagged_text_report': {
            'key': f'hard-to-read-sentences/record/slate-api/{record}',
            'kind': 'flagged-text',
            'machine': 'k3s-node',
            'is_machine_from_sender': True,
            'title': f'{record}: 1 hard-to-read sentence in slate.title',
            'source': 'hard-to-read-sentences',
            'detail': refusal,
            'reported_ts': '2026-11-26T08:00:00.000Z',
            'priority': 'low',
            'data': {
                'door': 'slate-api',
                'record': record,
                'flag': 'deny',
                'labels': {label: request[label] for label in ('client_id', 'flow_id') if label in request},
                'fields': [{'field': 'slate.title', 'text': title, 'sentences': [sentence]}],
            },
        },
    }


def collected_records(received: list[Received]) -> list[tuple[dict[str, str], str]]:
    """Each OTLP log record the collector took, as its string attributes and its Body."""
    records = []
    for request in received:
        body = gzip.decompress(request.body) if request.headers.get('content-encoding') == 'gzip' else request.body
        export = ExportLogsServiceRequest.FromString(body)
        for resource_logs in export.resource_logs:
            for scope_logs in resource_logs.scope_logs:
                for record in scope_logs.log_records:
                    attributes = {kv.key: kv.value.string_value for kv in record.attributes}
                    records.append((attributes, record.body.string_value))
    return records


@dc.dataclass
class Scenario:
    client: Any
    standin: list[Received]
    collector: list[Received]
    finish: Callable[[], None]

    def posts_to(self, path: str) -> list[Received]:
        return [request for request in self.standin if request.path == path]


@contextmanager
def scenario(find: Route) -> Iterator[Scenario]:
    """slate's API as `SCRIPT`, over a `/find` stand-in that also serves the identity provider and the inbox, and an OTLP collector."""

    def discovery(_request: Received) -> tuple[int, Any]:
        return 200, {
            'issuer': standin_url,
            'token_endpoint': f'{standin_url}/token',
            'device_authorization_endpoint': f'{standin_url}/device',
        }

    routes: dict[tuple[str, str], Route] = {
        ('POST', '/find'): find,
        ('GET', '/.well-known/openid-configuration'): discovery,
        ('POST', '/token'): lambda _r: (200, {'access_token': INBOX_TOKEN, 'token_type': 'Bearer', 'expires_in': 300}),
        ('POST', '/inbox/reports'): lambda r: (201, json.loads(r.body)),
    }
    with serving(routes) as (standin_url, standin), serving({('POST', '/v1/logs'): lambda _r: (200, b'')}) as (collector_url, collector):
        settings = copy.deepcopy(test_settings)
        settings.oidc.issuer = standin_url
        settings.hard_to_read_sentences.url = standin_url
        settings.report_inbox.api_url = standin_url
        settings.report_inbox.client_id = 'slate-inbox-reporter'
        settings.report_inbox.client_secret = 'inbox-client-secret'
        logger_provider = new_logger_provider([OTLPLogExporter(endpoint=f'{collector_url}/v1/logs')])
        api = create_api(settings, logger_provider=logger_provider)

        def finish() -> None:
            api.state.hard_to_read_sentence_checks.stop(30)
            logger_provider.force_flush()

        with transactional_api_client(api, caller=lambda: SCRIPT) as (client, _session):
            yield Scenario(client, standin, collector, finish)
        logger_provider.shutdown()


def file_as_the_script(client: Any, title: str) -> dict[str, Any]:
    response = client.post('/issues', json={'title': title, 'status': 'incoming'})
    assert response.status_code == status.HTTP_201_CREATED, show_status_and_response(response)
    return response.json()['issue']


def test_a_scripts_flagged_title_is_written_and_its_report_filed_once():
    with scenario(lambda r: (200, flagged_answer(json.loads(r.body)))) as run:
        issue = file_as_the_script(run.client, FLAGGED_TITLE)
        flow = run.client.get(f'/records/{issue["number"]}').json()['record']['flow_id']
        run.finish()

    [find] = run.posts_to('/find')
    assert json.loads(find.body) == {
        'door': 'slate-api',
        'fields': {'slate.title': FLAGGED_TITLE},
        'record': str(issue['number']),
        'client_id': SCRIPT.client_id,
        'flow_id': flow,
    }
    [token] = run.posts_to('/token')
    assert parse_qs(token.body.decode()) == {'grant_type': ['client_credentials'], 'scope': ['fleet.inbox.record']}
    [filed] = run.posts_to('/inbox/reports')
    assert filed.headers['authorization'] == f'Bearer {INBOX_TOKEN}'
    assert json.loads(filed.body) == [flagged_answer(json.loads(find.body))['flagged_text_report']]
    assert collected_records(run.collector) == []


def test_a_scripts_title_is_written_when_its_check_gets_no_answer_and_one_unanswered_record_is_sent():
    with scenario(lambda _r: (503, {'error': 'Service Unavailable'})) as run:
        issue = file_as_the_script(run.client, FLAGGED_TITLE)
        assert run.client.get(f'/issues/{issue["number"]}').json()['title'] == FLAGGED_TITLE
        run.finish()

    [(attributes, body)] = collected_records(run.collector)
    expected = {
        'fleet.kind': 'hard_to_read_sentences.check',
        f'{CHECK}.door': 'slate-api',
        f'{CHECK}.field': 'slate.title',
        f'{CHECK}.result': 'unanswered',
        f'{CHECK}.record': str(issue['number']),
        f'{CHECK}.client_id': SCRIPT.client_id,
        f'{CHECK}.why_unchecked': 'the service answered 503',
    }
    assert {name: attributes.get(name) for name in expected} == expected
    assert body == '[]'
    assert run.posts_to('/inbox/reports') == []
