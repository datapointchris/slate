from typing import Annotated

from pydantic import BaseModel
from pydantic import ConfigDict
from pydantic import Field
from pydantic import StringConstraints

# A record named by its number, as `42` or `#42`. A CLI forwards what was typed, so a number
# arrives as a string as often as an int.
RecordRef = int | str

Priority = Annotated[int, Field(ge=0, le=4, description='0 none, 1 urgent, 2 high, 3 medium, 4 low')]

# Who acted, as the client names them: `claude-code/<session>`, `worker/<name>`, `user@host`.
Actor = Annotated[str, StringConstraints(strip_whitespace=True, min_length=1)]


class RecordModel(BaseModel):
    """A schema read from a model row's attributes, which every schema of a record is."""

    model_config = ConfigDict(from_attributes=True)
