"""Every side effect and warning of a write, gathered while it runs and answered with it.

Nothing in slate happens implicitly. A write that changes anything its request did not name says
so in its answer, and so does a write that went through a conflict a caller should look at. A
handler takes `Effects`, passes it to whatever does the work, and answers `effects.answer(...)`.
"""

import enum
from typing import Annotated

import structlog
from fastapi import Depends

from slate.schemas.write_effects import WriteEffect
from slate.schemas.write_effects import Written

logger = structlog.get_logger()


class WriteEffectCode(enum.StrEnum):
    """The key of each side effect and warning, so a client and a test branch on it rather than on its sentence."""

    # Side effects: a change the request did not name.
    ANCESTOR_REOPENED = 'ancestor_reopened'


class WriteEffects:
    """The side effects and warnings of one request's write. FastAPI builds one per request."""

    def __init__(self) -> None:
        self.side_effects: list[WriteEffect] = []
        self.warnings: list[WriteEffect] = []

    def did(self, code: WriteEffectCode, message: str, record: int | None = None) -> None:
        self.side_effects.append(WriteEffect(code=code, message=message, record=record))
        logger.info('write_side_effect', code=code, record=record)

    def warn(self, code: WriteEffectCode, message: str, record: int | None = None) -> None:
        self.warnings.append(WriteEffect(code=code, message=message, record=record))
        logger.info('write_warning', code=code, record=record)

    def answer[W: Written](self, schema: type[W], **written: object) -> W:
        return schema(side_effects=self.side_effects, warnings=self.warnings, **written)


Effects = Annotated[WriteEffects, Depends(WriteEffects)]
