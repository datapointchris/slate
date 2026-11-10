import pytest
from sqlalchemy import select

from slate import models
from slate.models.claim import CLAIM_ENDINGS
from slate.models.document import DOCUMENT_KINDS
from slate.models.record import Kind
from slate.models.retired_direction_tables import RETIRED_RECORD_KIND
from slate.models.worklog import WORKLOG_LINK_KINDS
from slate.models.worklog import WORKLOG_OUTCOMES
from slate.models.worklog import WORKLOG_STEPS


@pytest.mark.parametrize(
    ('lookup', 'declared'),
    [
        (models.RecordKind, [*Kind, RETIRED_RECORD_KIND]),
        (models.ClaimEnding, CLAIM_ENDINGS),
        (models.WorklogOutcome, WORKLOG_OUTCOMES),
        (models.WorklogStep, WORKLOG_STEPS),
        (models.WorklogLinkKind, WORKLOG_LINK_KINDS),
        (models.DocumentKind, DOCUMENT_KINDS),
    ],
    ids=['record_kinds', 'claim_endings', 'worklog_outcomes', 'worklog_steps', 'worklog_link_kinds', 'document_kinds'],
)
def test_the_migrated_lookup_rows_are_the_vocabulary_the_code_writes(txn_api, lookup, declared):
    """A value the code writes with no row fails its foreign key at commit, so the close or hold writing it answers 500."""
    _, session = txn_api
    assert set(session.scalars(select(lookup.name)).all()) == set(declared)


def test_the_step_rows_hold_their_positions_in_the_order_the_code_lists_them(txn_api):
    """The marks and the stage read the code's order, so a position the table moved would draw one order and stage by another."""
    _, session = txn_api
    assert session.scalars(select(models.WorklogStep.name).order_by(models.WorklogStep.position)).all() == WORKLOG_STEPS
