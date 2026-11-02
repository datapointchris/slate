"""A timezone named the IANA way, such as `America/New_York`, never as an offset.

An event's zone has to say what its venue's clock reads next March, and a
request's zone has to say where a day ends after the next daylight-saving
change. An offset records one moment's distance from UTC and answers neither.
"""

from functools import cache
from importlib import resources
from typing import Annotated

from pydantic import AfterValidator


@cache
def iana_zone_names() -> frozenset[str]:
    """The zone names the IANA database publishes, as the `tzdata` package lists them.

    Loading the name with `ZoneInfo` is not the same test. It reads any TZif file
    under the host's zoneinfo directory, and Ubuntu's includes `localtime`, a link
    to `/etc/localtime`, so the answer would change with the machine.
    """
    listing = resources.files('tzdata').joinpath('zones').read_text()
    return frozenset(line.strip() for line in listing.splitlines() if line.strip())


def refuse_unknown_zone(name: str) -> str:
    if name not in iana_zone_names():
        raise ValueError(f'{name!r} is not an IANA timezone name, e.g. America/New_York')
    return name


IanaZone = Annotated[str, AfterValidator(refuse_unknown_zone)]
