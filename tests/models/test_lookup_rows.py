import pytest
from sqlalchemy import select

from slate import models
from slate.models.claim import CLAIM_ENDINGS
from slate.models.document import DOCUMENT_KINDS
from slate.models.record import Kind
from slate.models.worklog import WORKLOG_LINK_KINDS
from slate.models.worklog import WORKLOG_OUTCOMES
from slate.models.worklog import WORKLOG_WORKFLOWS


@pytest.mark.parametrize(
    ('lookup', 'declared'),
    [
        (models.RecordKind, list(Kind)),
        (models.ClaimEnding, CLAIM_ENDINGS),
        (models.WorklogOutcome, WORKLOG_OUTCOMES),
        (models.WorklogWorkflow, WORKLOG_WORKFLOWS),
        (models.WorklogLinkKind, WORKLOG_LINK_KINDS),
        (models.DocumentKind, DOCUMENT_KINDS),
    ],
    ids=['record_kinds', 'claim_endings', 'worklog_outcomes', 'worklog_workflows', 'worklog_link_kinds', 'document_kinds'],
)
def test_the_migrated_lookup_rows_are_the_vocabulary_the_code_writes(txn_api, lookup, declared):
    """A value the code writes with no row fails its foreign key at commit, so the close or hold writing it answers 500."""
    _, session = txn_api
    assert set(session.scalars(select(lookup.name)).all()) == set(declared)
