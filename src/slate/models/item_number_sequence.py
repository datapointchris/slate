from sqlalchemy import Sequence

from slate.database.base import Base

# One sequence numbers issues and initiatives alike, so a bare number names
# exactly one row whichever table holds it.
ITEM_NUMBER_SEQUENCE = Sequence('item_numbers', metadata=Base.metadata)
