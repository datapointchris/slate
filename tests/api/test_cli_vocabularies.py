"""The closed vocabularies the CLI keeps its own copy of, written where the CLI's suite reads them."""

import json
from pathlib import Path
from typing import get_args

import pytest

from slate.api.endpoints.claims import ClaimStateFilter
from slate.api.endpoints.worklogs import WorklogStateFilter
from slate.models.worklog import WORKLOG_LINK_KINDS
from slate.models.worklog import WORKLOG_WORKFLOWS

VOCABULARIES_DOCUMENT = Path(__file__).parent / 'testdata' / 'cli-vocabularies.json'


def test_the_document_the_cli_reads_holds_the_vocabularies_it_copies():
    """The CLI refuses a value outside its copy before sending it, so a copy left behind refuses what the API takes.

    A stale file is rewritten here and the test fails, so the change is committed beside the vocabulary.
    """
    table = {
        'claim_states': list(get_args(ClaimStateFilter)),
        'worklog_states': list(get_args(WorklogStateFilter)),
        'worklog_workflows': WORKLOG_WORKFLOWS,
        'worklog_link_kinds': WORKLOG_LINK_KINDS,
    }
    document = json.dumps(table, indent=2) + '\n'
    if not VOCABULARIES_DOCUMENT.exists() or VOCABULARIES_DOCUMENT.read_text() != document:
        VOCABULARIES_DOCUMENT.write_text(document)
        pytest.fail(f'{VOCABULARIES_DOCUMENT} did not hold the vocabularies the CLI copies and has been rewritten; commit it')
