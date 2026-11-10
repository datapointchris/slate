from slate.models.agent_session import AgentSession
from slate.models.agent_session import MessageTokens
from slate.models.claim import Claim
from slate.models.claim import ClaimEnding
from slate.models.claim import ClaimWait
from slate.models.comment import Comment
from slate.models.direction import Direction
from slate.models.direction import DirectionStatus
from slate.models.direction import RecordDirection
from slate.models.document import Document
from slate.models.document import DocumentKind
from slate.models.document import DocumentVersion
from slate.models.idea import Idea
from slate.models.idea import IdeaEvidence
from slate.models.initiative import Initiative
from slate.models.initiative import InitiativeDependency
from slate.models.initiative import InitiativeStatus
from slate.models.issue import Issue
from slate.models.issue import IssueDependency
from slate.models.issue import IssueLabel
from slate.models.issue import IssueLabelAssignment
from slate.models.issue import IssueStatus
from slate.models.issue import IssueType
from slate.models.record import Kind
from slate.models.record import Record
from slate.models.record import RecordKind
from slate.models.record_change import RecordChange
from slate.models.tag import RecordTag
from slate.models.tag import Tag
from slate.models.worklog import Worklog
from slate.models.worklog import WorklogLink
from slate.models.worklog import WorklogLinkKind
from slate.models.worklog import WorklogOutcome
from slate.models.worklog import WorklogSpan
from slate.models.worklog import WorklogStep

# The table holding each kind of record, read from the kind each model declares.
KIND_MODELS: dict[Kind, type[Idea] | type[Initiative] | type[Issue] | type[Direction]] = {
    model.RECORD_KIND: model for model in (Idea, Initiative, Issue, Direction)
}

__all__ = [
    'KIND_MODELS',
    'AgentSession',
    'Claim',
    'ClaimEnding',
    'ClaimWait',
    'Comment',
    'Direction',
    'DirectionStatus',
    'Document',
    'DocumentKind',
    'DocumentVersion',
    'Idea',
    'IdeaEvidence',
    'Initiative',
    'InitiativeDependency',
    'InitiativeStatus',
    'Issue',
    'IssueDependency',
    'IssueLabel',
    'IssueLabelAssignment',
    'IssueStatus',
    'IssueType',
    'MessageTokens',
    'Record',
    'RecordChange',
    'RecordDirection',
    'RecordKind',
    'RecordTag',
    'Tag',
    'Worklog',
    'WorklogLink',
    'WorklogLinkKind',
    'WorklogOutcome',
    'WorklogSpan',
    'WorklogStep',
]
