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
from opentelemetry import trace

from slate.api.request_zone import CALENDAR_FALLBACK_ZONE
from slate.api.request_zone import RequestZone
from slate.schemas.write_effects import WriteEffect
from slate.schemas.write_effects import Written
from slate.tracing.named_work import RECORD_NUMBER

logger = structlog.get_logger()

WRITE_EFFECT_CODE = 'slate.write_effect.code'


class WriteEffectCode(enum.StrEnum):
    """The key of each side effect and warning, so a client and a test branch on it rather than on its sentence."""

    # Side effects: a change the request did not name.
    ANCESTOR_REOPENED = 'ancestor_reopened'
    CLAIM_ENDED = 'claim_ended'
    EXPIRED_CLAIM_ENDED = 'expired_claim_ended'
    INITIATIVE_CREATED = 'initiative_created'
    PIN_CLEARED = 'pin_cleared'
    PIN_CARRIED = 'pin_carried'

    # Warnings on a pin write that changed nothing.
    ALREADY_PINNED = 'already_pinned'
    NOT_PINNED = 'not_pinned'

    # Side effects on worklogs and a claim's waits.
    WORKLOG_OPENED = 'worklog_opened'
    WORKLOG_ENDED = 'worklog_ended'
    SESSION_LEFT_WORKLOG = 'session_left_worklog'
    SESSION_REJOINED_WORKLOG = 'session_rejoined_worklog'
    WAIT_ENDED = 'wait_ended'

    # Side effects of deleting a record: what went with it, and each other record it changed.
    CLAIMS_DELETED = 'claims_deleted'
    WORKLOGS_DELETED = 'worklogs_deleted'
    DOCUMENTS_DELETED = 'documents_deleted'
    COMMENTS_DELETED = 'comments_deleted'
    CHILD_DETACHED = 'child_detached'
    DEPENDENCY_REMOVED = 'dependency_removed'
    ISSUE_DETACHED = 'issue_detached'

    # A side effect of deleting a label: each issue it is taken off.
    LABEL_REMOVED = 'label_removed'

    # A warning on deleting a record another holder's live claim was on.
    DELETED_ANOTHERS_CLAIM = 'deleted_anothers_claim'

    # A warning on linking evidence an idea already links.
    EVIDENCE_ALREADY_LINKED = 'evidence_already_linked'

    # A warning on creating a record whose title reads like an open record's.
    POSSIBLE_DUPLICATE = 'possible_duplicate'

    # Warnings on a claim write that changed nothing, or went through without the lease asked for.
    ALREADY_HELD = 'already_held'
    CLAIM_HAD_EXPIRED = 'claim_had_expired'
    NOTHING_READY = 'nothing_ready'
    PASSED_OVER_PLAN = 'passed_over_plan'
    NOTHING_TO_RELEASE = 'nothing_to_release'
    RELEASED_ANOTHERS_CLAIM = 'released_anothers_claim'
    ALREADY_WAITING = 'already_waiting'
    NOT_WAITING = 'not_waiting'

    # Warnings on a worklog write that changed nothing, or went through beside something open.
    ALREADY_JOINED = 'already_joined'
    ALREADY_LINKED = 'already_linked'
    WORKLOG_ALREADY_ENDED = 'worklog_already_ended'
    CLAIM_STILL_HELD = 'claim_still_held'
    OPEN_WORKLOG = 'open_worklog'
    # A worklog edit that left an estimate on a worklog no total reads it from.
    ESTIMATE_OFF_PREFLIGHT = 'estimate_off_preflight'

    # Warnings on a usage report: it matched what slate holds, it read less of the session than
    # slate holds, or another session reported some of its messages first.
    USAGE_UNCHANGED = 'usage_unchanged'
    USAGE_STALE = 'usage_stale'
    MESSAGES_HELD_BY_ANOTHER_SESSION = 'messages_held_by_another_session'

    # Warnings on writing a document version that wrote nothing, or names no worklog.
    DOCUMENT_UNCHANGED = 'document_unchanged'
    NO_WORKLOG = 'no_worklog'

    # Warnings on a hold, about the record held and its neighbors.
    RECORD_CLOSED = 'record_closed'
    IN_TRIAGE = 'in_triage'
    DEFERRED = 'deferred'
    SHELVED = 'shelved'
    IDEA_PROMOTED = 'idea_promoted'
    OPEN_DEPENDENCY = 'open_dependency'
    INITIATIVE_WAITS = 'initiative_waits'
    OPEN_CHILDREN = 'open_children'
    CLAIMED_INITIATIVE = 'claimed_initiative'
    CLAIMED_ISSUE_IN_INITIATIVE = 'claimed_issue_in_initiative'
    CLAIMED_PARENT = 'claimed_parent'
    CLAIMED_CHILD = 'claimed_child'
    CLAIMED_DEPENDENT = 'claimed_dependent'
    CLAIMED_OVERLAPPING_PATHS = 'claimed_overlapping_paths'


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
        """The write's answer. Each side effect and warning also becomes an event on the request's span.

        An event carries the effect's code and record, so a trace names every record a write changed
        beside the one it was asked about. Its message stays off the span, since it quotes titles.
        """
        span = trace.get_current_span()
        for event, effects in (('slate.write_side_effect', self.side_effects), ('slate.write_warning', self.warnings)):
            for effect in effects:
                attributes: dict[str, str | int] = {WRITE_EFFECT_CODE: effect.code}
                if effect.record is not None:
                    attributes[RECORD_NUMBER] = effect.record
                span.add_event(event, attributes)
        return schema(side_effects=self.side_effects, warnings=self.warnings, **written)


def request_effects(zone: RequestZone) -> WriteEffects:
    return WriteEffects(zone)


Effects = Annotated[WriteEffects, Depends(request_effects)]
