"""The IANA zone a request's calendar questions are answered in.

"Which day is today" has no answer without a zone. A caller naming one in `timezone` gets it, and
the CLI sends the zone of the machine it runs on. A caller naming none gets UTC.
"""

from typing import Annotated

from fastapi import Depends
from fastapi import Query

from slate.schemas.iana_zone import IanaZone

CALENDAR_FALLBACK_ZONE = 'UTC'


def request_zone(
    timezone: Annotated[IanaZone | None, Query(description='IANA zone for the calendar; UTC where none is named')] = None,
) -> str:
    return timezone or CALENDAR_FALLBACK_ZONE


RequestZone = Annotated[str, Depends(request_zone)]
