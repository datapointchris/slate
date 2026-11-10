import enum
from collections.abc import Sequence
from typing import Any
from uuid import UUID

from fastapi import HTTPException
from fastapi import status


class Refusal(enum.StrEnum):
    """The reasons this API refuses a request, as keys rather than sentences.

    A test asserting the sentence pins the wording, so rephrasing a refusal
    breaks tests that were never about the words. Naming the member and keeping
    the wording here means one edit moves both.

    A refusal carrying a value the caller supplied is not a member, since the
    text differs per call. Assert the interpolated value beside a stable
    condition instead.
    """

    INVALID_CREDENTIALS = 'Invalid credentials'
    OUTSIDE_CLIENT_SCOPES = "Route is outside this client's scopes"
    SCOPED_CLIENT_CLAIMS_AS_ITSELF = 'A service client may only hold, extend, release, wait on or resume a claim under its own client id'
    SCOPED_CLIENT_WRITES_ITS_OWN_WORKLOGS = 'A service client may only join, edit or finish a worklog it opened'
    SCOPED_CLIENT_FILES_ONLY_INTO_TRIAGE = 'A service client may only file an issue into triage, under no parent'
    SCOPED_CLIENT_ONLY_CLOSES = 'A service client may only close an open issue'
    SCOPED_CLIENT_ONLY_SETS_PLAN_FIELDS = "A service client's edit may only set an issue's priority, initiative, acceptance, paths and tags"
    SCOPED_CLIENT_RELEASES_ITS_OWN = 'A service client may only release a claim it holds'
    SCOPED_CLIENT_WRITES_WORKLOGS_AS_ITSELF = 'A service client may only start, finish or link a worklog under its own client id'


class NotFoundException(HTTPException):
    """Exception raised when a requested resource is not found.

    `resource_type` is the noun a person reads, so it is written as words:
    'issue label', not 'issue_label'. The CLI prints the detail verbatim to a
    caller who is stuck, and an internal spelling there names something they
    cannot look up or type.

    `near` is for a resource addressed by name: the names the reference most
    likely meant, said on the same line, since a name matches only in full.

    `held_as` is the kind of record that holds a number asked for as another
    kind, so `issue 1986 not found` says that #1986 is an initiative.
    """

    def __init__(
        self,
        resource_type: str,
        resource_id: str | int | UUID,
        logger: Any,
        near: Sequence[str] = (),
        held_as: str | None = None,
    ):
        logger.warning('resource_not_found', resource_type=resource_type, resource_id=resource_id, held_as=held_as)
        detail = f'{resource_type} {resource_id} not found'
        if held_as is not None:
            detail += f': #{str(resource_id).removeprefix("#")} is an {held_as}'
        if near:
            detail += '. A name matches only in full, and these come near it: ' + ', '.join(repr(name) for name in near)
        super().__init__(status_code=status.HTTP_404_NOT_FOUND, detail=detail)


class UnauthorizedException(HTTPException):
    """Exception raised when authentication fails or access is unauthorized."""

    def __init__(self, reason: str, logger: Any):
        logger.warning('unauthorized', reason=reason)
        super().__init__(status_code=status.HTTP_401_UNAUTHORIZED, detail=f'Unauthorized: {reason}')


class ForbiddenException(HTTPException):
    """Exception raised when access to a resource is forbidden."""

    def __init__(self, reason: str, logger: Any):
        logger.warning('access_denied', reason=reason)
        super().__init__(status_code=status.HTTP_403_FORBIDDEN, detail=f'Access denied: {reason}')
