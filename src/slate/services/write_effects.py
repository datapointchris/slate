"""Every side effect and warning of a write, gathered while it runs and answered with it.

Nothing in slate happens implicitly. A write that changes anything its request did not name says
so in its answer, and so does a write that went through a conflict a caller should look at. A
handler takes `Effects`, passes it to whatever does the work, and answers `effects.answer(...)`.
"""

import datetime as dt
import enum
from typing import Annotated
from zoneinfo import ZoneInfo

import structlog
from fastapi import Depends

from slate.api.request_zone import CALENDAR_FALLBACK_ZONE
from slate.api.request_zone import RequestZone
from slate.schemas.write_effects import WriteEffect
from slate.schemas.write_effects import Written

logger = structlog.get_logger()


class WriteEffectCode(enum.StrEnum):
    """The key of each side effect and warning, so a client and a test branch on it rather than on its sentence."""

    # Side effects: a change the request did not name.
    ANCESTOR_REOPENED = 'ancestor_reopened'
    CLAIM_ENDED = 'claim_ended'
    EXPIRED_CLAIM_ENDED = 'expired_claim_ended'
    INITIATIVE_CREATED = 'initiative_created'

    # Side effects of deleting a record: what went with it, and each other record it changed.
    CLAIMS_DELETED = 'claims_deleted'
    CHILD_DETACHED = 'child_detached'
    DEPENDENCY_REMOVED = 'dependency_removed'
    ISSUE_DETACHED = 'issue_detached'

    # A warning on deleting a record another holder's live claim was on.
    DELETED_ANOTHERS_CLAIM = 'deleted_anothers_claim'

    # A warning on linking evidence an idea already links.
    EVIDENCE_ALREADY_LINKED = 'evidence_already_linked'

    # Warnings on a claim write that changed nothing, or went through without the lease asked for.
    ALREADY_HELD = 'already_held'
    CLAIM_HAD_EXPIRED = 'claim_had_expired'
    NOTHING_READY = 'nothing_ready'
    NOTHING_TO_RELEASE = 'nothing_to_release'
    RELEASED_ANOTHERS_CLAIM = 'released_anothers_claim'

    # Warnings on a hold, about the record held and its neighbors.
    RECORD_CLOSED = 'record_closed'
    IN_TRIAGE = 'in_triage'
    DEFERRED = 'deferred'
    SHELVED = 'shelved'
    IDEA_PROMOTED = 'idea_promoted'
    OPEN_DEPENDENCY = 'open_dependency'
    OPEN_CHILDREN = 'open_children'
    CLAIMED_INITIATIVE = 'claimed_initiative'
    CLAIMED_ISSUE_IN_INITIATIVE = 'claimed_issue_in_initiative'
    CLAIMED_PARENT = 'claimed_parent'
    CLAIMED_CHILD = 'claimed_child'
    CLAIMED_DEPENDENT = 'claimed_dependent'


class WriteEffects:
    """The side effects and warnings of one request's write, written for a reader in `zone`. FastAPI builds one per request."""

    def __init__(self, zone: str = CALENDAR_FALLBACK_ZONE) -> None:
        self.zone = zone
        self.side_effects: list[WriteEffect] = []
        self.warnings: list[WriteEffect] = []

    def moment(self, ts: dt.datetime) -> str:
        """`ts` to the minute on the reader's clock, naming the zone, so a message agrees with the times the CLI prints."""
        return f'{ts.astimezone(ZoneInfo(self.zone)):%Y-%m-%d %H:%M %Z}'

    def attempt(self) -> WriteEffects:
        """A gathering for one attempt, on the same clock, that `merge` takes on once the attempt succeeds."""
        return WriteEffects(self.zone)

    def did(self, code: WriteEffectCode, message: str, record: int | None = None) -> None:
        self.side_effects.append(WriteEffect(code=code, message=message, record=record))
        logger.info('write_side_effect', code=code, record=record)

    def warn(self, code: WriteEffectCode, message: str, record: int | None = None) -> None:
        self.warnings.append(WriteEffect(code=code, message=message, record=record))
        logger.info('write_warning', code=code, record=record)

    def merge(self, other: WriteEffects) -> None:
        """Take on what an attempt gathered once it succeeds, so a rolled-back attempt reports nothing."""
        self.side_effects.extend(other.side_effects)
        self.warnings.extend(other.warnings)

    def answer[W: Written](self, schema: type[W], **written: object) -> W:
        return schema(side_effects=self.side_effects, warnings=self.warnings, **written)


def request_effects(zone: RequestZone) -> WriteEffects:
    return WriteEffects(zone)


Effects = Annotated[WriteEffects, Depends(request_effects)]
