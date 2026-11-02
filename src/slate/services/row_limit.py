"""Row capping for the collection reads, with one meaning for zero.

Every list over data that grows outside the binary takes a limit, which is every
collection this API answers. It is applied here rather than in each client
because the rows a caller discards are rows nobody had to serialize.

**Absence means no cap and zero means no rows.** A reserved value has to be one
no caller could have meant, and `tail -n 0` settles that a caller can mean zero
rows. So `limit` omitted leaves the query uncapped, and `limit=0` reaches
`LIMIT 0` and returns an empty set. One helper is what keeps every endpoint on
the same side of that, and a test walks the routes rather than trusting it.

A negative limit is rejected at the edge by `ge=0` rather than reaching SQL.
"""

from typing import Annotated

from fastapi import Query
from sqlalchemy import Select

RowLimit = Annotated[
    int | None,
    Query(ge=0, description='Return at most this many rows. Omit for every row; 0 returns none.'),
]

# A read whose subject grows without end carries a number instead of absence, so
# it has no way to spell "every row" and needs none. The test is whether the
# hidden class outgrows the visible one left alone for a year, and a job run
# history is the case that passes it. The floor is the half both share: a
# negative reaches `Select.limit` as `LIMIT -1` wherever `ge=0` is missing.
CappedRowLimit = Annotated[
    int,
    Query(ge=0, description='Return at most this many rows. 0 returns none.'),
]


def apply_row_limit(query: Select, limit: int | None) -> Select:
    """Cap the query at `limit` rows, or leave it alone when there is no cap.

    `None` is the absence of a cap and is the only thing that asks for every
    row. `0` is a row count like any other, so it reaches `LIMIT 0`. A falsy
    test cannot tell the two apart, and it answers the caller that asked for
    nothing with the whole collection.
    """
    if limit is None:
        return query
    return query.limit(limit)
