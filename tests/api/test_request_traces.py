"""A request continues its caller's trace, and its span and request line name the record it is about."""

from fastapi import status
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter
from opentelemetry.trace import SpanKind
from opentelemetry.trace import format_span_id
from opentelemetry.trace import format_trace_id
from sqlalchemy import select
from structlog.testing import capture_logs

from slate import models
from slate.api.main import create_api
from slate.tracing.tracer_provider import new_tracer_provider
from tests.conftest import TEST_PERSON
from tests.conftest import transactional_api_client
from tests.session_database import test_settings
from tests.util import show_status_and_response

CALLER_TRACE_ID = '0af7651916cd43dd8448eb211c80319c'
CALLER_SPAN_ID = 'b7ad6b7169203331'


def test_a_hold_continues_the_callers_trace_and_names_its_record_worklog_and_baggage_on_the_span_and_audit_row():
    exporter = InMemorySpanExporter()
    provider = new_tracer_provider([exporter])
    with transactional_api_client(create_api(test_settings, provider), caller=lambda: TEST_PERSON) as (client, session):
        number = client.post('/issues', json={'title': 'Follow a hold through its trace'}).json()['issue']['number']
        response = client.post(
            '/claims',
            json={'record': number, 'holder': 'claude-code/tracer'},
            headers={'traceparent': f'00-{CALLER_TRACE_ID}-{CALLER_SPAN_ID}-01', 'baggage': 'flow.id=flow-7'},
        )
        assert response.status_code == status.HTTP_200_OK, show_status_and_response(response)
        provider.force_flush()
        audited = session.scalar(
            select(models.RecordChange.traceparent)
            .where(models.RecordChange.table_name == 'claims', models.RecordChange.operation == 'insert')
            .order_by(models.RecordChange.id.desc())
        )

    hold = next(span for span in exporter.get_finished_spans() if span.kind == SpanKind.SERVER and span.name == 'POST /api/v1/claims')
    assert format_trace_id(hold.context.trace_id) == CALLER_TRACE_ID
    assert hold.parent is not None and format_span_id(hold.parent.span_id) == CALLER_SPAN_ID
    assert hold.attributes['slate.record.number'] == number
    assert hold.attributes['slate.worklog'] == f'{number}.1'
    assert hold.attributes['flow.id'] == 'flow-7'
    assert ('slate.write_side_effect', 'worklog_opened', number) in [
        (event.name, event.attributes['slate.write_effect.code'], event.attributes.get('slate.record.number')) for event in hold.events
    ]
    assert audited == f'00-{CALLER_TRACE_ID}-{format_span_id(hold.context.span_id)}-01'


def test_the_request_line_names_its_record_and_worklog_when_the_callers_sampler_dropped_the_trace():
    """A caller whose sampler dropped the trace sends flags `00`, so the server span records nothing."""
    with transactional_api_client(create_api(test_settings, new_tracer_provider([])), caller=lambda: TEST_PERSON) as (client, _):
        number = client.post('/issues', json={'title': 'Name the record on an unsampled request'}).json()['issue']['number']
        with capture_logs() as lines:
            response = client.post(
                '/claims',
                json={'record': number, 'holder': 'claude-code/tracer'},
                headers={'traceparent': f'00-{CALLER_TRACE_ID}-{CALLER_SPAN_ID}-00'},
            )
    assert response.status_code == status.HTTP_200_OK, show_status_and_response(response)

    request_line = next(line for line in lines if line['event'] == 'request_completed')
    assert (request_line['record'], request_line['worklog']) == (number, f'{number}.1')
