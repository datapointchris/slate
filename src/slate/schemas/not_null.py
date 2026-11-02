"""A partial-update field over a NOT NULL column: it may be omitted, never nulled.

`name: NotNull[str] = None` leaves an omitted field at a default Pydantic does
not validate, so `model_dump(exclude_unset=True)` drops it and the column is left
alone. An explicit null is validated and refused with a 422 naming the field,
where it would otherwise reach the column and come back as a 500.
"""

from typing import Annotated
from typing import TypeVar

from pydantic import AfterValidator

T = TypeVar('T')


def refuse_null[V](value: V | None) -> V:
    if value is None:
        raise ValueError('cannot be null; omit the field to leave it unchanged')
    return value


NotNull = Annotated[T | None, AfterValidator(refuse_null)]
