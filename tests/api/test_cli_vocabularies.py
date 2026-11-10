"""The closed vocabularies the CLI keeps its own copy of, written where the CLI's suite reads them."""

import json
from pathlib import Path
from typing import get_args

import pytest

from slate.api.endpoints import issues
from slate.api.endpoints.claims import ClaimStateFilter
from slate.api.endpoints.initiatives import InitiativeSort
from slate.api.endpoints.worklogs import WorklogStateFilter
from slate.models.direction import DIRECTION_STATUSES
from slate.models.document import DOCUMENT_KINDS
from slate.models.initiative import INITIATIVE_STATUSES
from slate.models.initiative import TERMINAL_INITIATIVE_STATUSES
from slate.models.issue import ISSUE_STATUSES
from slate.models.issue import ISSUE_TYPES
from slate.models.record import PRIORITIES
from slate.models.record import Kind
from slate.models.worklog import WORKLOG_LINK_KINDS
from slate.models.worklog import WORKLOG_STEPS
from slate.schemas.queue import Step
from slate.services import initiatives

VOCABULARIES_DOCUMENT = Path(__file__).parent / 'testdata' / 'cli-vocabularies.json'


def test_the_document_the_cli_reads_holds_the_vocabularies_it_copies():
    """The CLI refuses a value outside its copy before sending it, so a copy left behind refuses what the API takes.

    A stale file is rewritten here and the test fails, so the change is committed beside the vocabulary.
    """
    table = {
        'claim_states': list(get_args(ClaimStateFilter)),
        'worklog_states': list(get_args(WorklogStateFilter)),
        'worklog_steps': WORKLOG_STEPS,
        'worklog_link_kinds': WORKLOG_LINK_KINDS,
        'issue_statuses': [*ISSUE_STATUSES, issues.ALL_STATUSES],
        'issue_types': ISSUE_TYPES,
        'priority_names': [PRIORITIES[value] for value in sorted(PRIORITIES)],
        'initiative_statuses': [*INITIATIVE_STATUSES, initiatives.ALL_STATUSES],
        'initiative_sorts': list(get_args(InitiativeSort)),
        'initiative_statuses_closed': TERMINAL_INITIATIVE_STATUSES,
        'direction_statuses': [*DIRECTION_STATUSES, initiatives.ALL_STATUSES],
        'record_kinds': [kind.value for kind in Kind],
        'document_kinds': DOCUMENT_KINDS,
        'queue_steps': list(get_args(Step)),
    }
    document = json.dumps(table, indent=2) + '\n'
    if not VOCABULARIES_DOCUMENT.exists() or VOCABULARIES_DOCUMENT.read_text() != document:
        VOCABULARIES_DOCUMENT.write_text(document)
        pytest.fail(f'{VOCABULARIES_DOCUMENT} did not hold the vocabularies the CLI copies and has been rewritten; commit it')
