"""The `since` and `until` query parameters every read bounded by time takes.

Each is an RFC 3339 instant with an offset. A value with no offset names no
instant, so it is a 422 rather than a guess at the caller's zone.
"""

from typing import Annotated

from fastapi import Depends
from fastapi import HTTPException
from fastapi import Query
from fastapi import status
from pydantic import AwareDatetime

from slate.services.time_window import TimeWindow


def time_window(
    since: Annotated[AwareDatetime | None, Query(description='First instant inside the window, RFC 3339 with an offset')] = None,
    until: Annotated[AwareDatetime | None, Query(description='First instant past the window, RFC 3339 with an offset')] = None,
) -> TimeWindow:
    if since is not None and until is not None and since > until:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail=f'since {since.isoformat()} is later than until {until.isoformat()}',
        )
    return TimeWindow(since=since, until=until)


TimeWindowQuery = Annotated[TimeWindow, Depends(time_window)]
