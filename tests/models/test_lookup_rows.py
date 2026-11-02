import pytest
from sqlalchemy import select

from slate import models
from slate.models.claim import CLAIM_ENDINGS
from slate.models.record import Kind


@pytest.mark.parametrize(
    ('lookup', 'declared'),
    [(models.RecordKind, list(Kind)), (models.ClaimEnding, CLAIM_ENDINGS)],
    ids=['record_kinds', 'claim_endings'],
)
def test_the_migrated_lookup_rows_are_the_vocabulary_the_code_writes(txn_api, lookup, declared):
    """A value the code writes with no row fails its foreign key at commit, so the close or hold writing it answers 500."""
    _, session = txn_api
    assert set(session.scalars(select(lookup.name)).all()) == set(declared)
