from slate.models.agent_session import AgentSession
from slate.models.agent_session import MessageTokens
from slate.models.claim import Claim
from slate.models.claim import ClaimEnding
from slate.models.claim import ClaimWait
from slate.models.comment import Comment
from slate.models.document import Document
from slate.models.document import DocumentKind
from slate.models.document import DocumentVersion
from slate.models.document_redirect import DocumentRedirect
from slate.models.focus import Focus
from slate.models.focus import FocusShare
from slate.models.idea import Idea
from slate.models.initiative import Initiative
from slate.models.initiative import InitiativeDependency
from slate.models.initiative import InitiativeStatus
from slate.models.initiative import InitiativeStepNotPlanned
from slate.models.issue import Issue
from slate.models.issue import IssueDependency
from slate.models.issue import IssueLabel
from slate.models.issue import IssueLabelAssignment
from slate.models.issue import IssueStatus
from slate.models.issue import IssueType
from slate.models.pursuit import Pursuit
from slate.models.pursuit import PursuitStatus
from slate.models.pursuit import RecordPursuit
from slate.models.record import Kind
from slate.models.record import Record
from slate.models.record import RecordKind
from slate.models.record_change import AuditTrailTable
from slate.models.record_change import RecordChange
from slate.models.record_check import CheckVerdict
from slate.models.record_check import RecordCheck
from slate.models.record_redirect import RecordRedirect
from slate.models.record_ref import RecordRef
from slate.models.tag import RecordTag
from slate.models.tag import Tag
from slate.models.worklog import Worklog
from slate.models.worklog import WorklogLink
from slate.models.worklog import WorklogLinkKind
from slate.models.worklog import WorklogOutcome
from slate.models.worklog import WorklogSpan
from slate.models.worklog import WorklogStep
from slate.models.worklog_redirect import WorklogRedirect

# The table holding each kind of record, read from the kind each model declares.
KIND_MODELS: dict[Kind, type[Idea] | type[Initiative] | type[Issue] | type[Pursuit]] = {
    model.RECORD_KIND: model for model in (Idea, Initiative, Issue, Pursuit)
}

__all__ = [
    'KIND_MODELS',
    'AgentSession',
    'AuditTrailTable',
    'CheckVerdict',
    'Claim',
    'ClaimEnding',
    'ClaimWait',
    'Comment',
    'Document',
    'DocumentKind',
    'DocumentRedirect',
    'DocumentVersion',
    'Focus',
    'FocusShare',
    'Idea',
    'Initiative',
    'InitiativeDependency',
    'InitiativeStatus',
    'InitiativeStepNotPlanned',
    'Issue',
    'IssueDependency',
    'IssueLabel',
    'IssueLabelAssignment',
    'IssueStatus',
    'IssueType',
    'MessageTokens',
    'Pursuit',
    'PursuitStatus',
    'Record',
    'RecordChange',
    'RecordCheck',
    'RecordKind',
    'RecordPursuit',
    'RecordRedirect',
    'RecordRef',
    'RecordTag',
    'Tag',
    'Worklog',
    'WorklogLink',
    'WorklogLinkKind',
    'WorklogOutcome',
    'WorklogRedirect',
    'WorklogSpan',
    'WorklogStep',
]
