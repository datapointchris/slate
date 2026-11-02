"""The names a reference that resolved to nothing most likely meant.

A resource addressed by its name is matched in full, so a short form or a typo
is a miss. The miss is answered with these rather than with a pointer at a
list command, because the resolver already holds the list.
"""

import difflib
from collections.abc import Iterable

NEAR_NAMES_SHOWN = 5


def names_near(typed: str, names: Iterable[str]) -> list[str]:
    """Names containing `typed`, then names a typo away from it, case-folded.

    Containment comes first because it is how a short form misses: `ypl` for
    `ypl — YouTube playlist CLI`. The order of `names` is kept within each
    group, so a caller passing active initiatives first sees them offered first.
    """
    by_folded: dict[str, str] = {}
    for name in names:
        by_folded.setdefault(name.casefold(), name)
    folded = typed.casefold()
    containing = [key for key in by_folded if folded in key]
    close = difflib.get_close_matches(folded, list(by_folded), n=NEAR_NAMES_SHOWN, cutoff=0.6)
    ordered = list(dict.fromkeys(containing + close))
    return [by_folded[key] for key in ordered[:NEAR_NAMES_SHOWN]]
