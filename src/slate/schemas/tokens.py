"""Tokens counted by kind, the one shape every read and write of usage carries."""

from __future__ import annotations

from pydantic import BaseModel
from pydantic import ConfigDict
from pydantic import NonNegativeInt


class TokensByKind(BaseModel):
    """Tokens by kind, never summed across kinds and never priced.

    `uncached_input` is input the model read without the prompt cache, `cache_write` input it wrote
    to the cache, `cache_read` input it read from the cache, and `output` what it wrote.
    """

    uncached_input: NonNegativeInt = 0
    cache_write: NonNegativeInt = 0
    cache_read: NonNegativeInt = 0
    output: NonNegativeInt = 0

    def __add__(self, other: TokensByKind) -> TokensByKind:
        """Each kind added to the same kind, so `sum(parts, TokensByKind())` totals parts kind by kind."""
        return TokensByKind(**{kind: getattr(self, kind) + getattr(other, kind) for kind in TokensByKind.model_fields})


class TokenEstimate(BaseModel):
    """The tokens by kind a piece of work is expected to spend, in the kinds `TokensByKind` counts.

    A kind left null was not estimated, which differs from an estimate of zero. A kind it does not
    name is refused, so a client sending one learns it was not applied.
    """

    model_config = ConfigDict(extra='forbid')

    uncached_input: NonNegativeInt | None = None
    cache_write: NonNegativeInt | None = None
    cache_read: NonNegativeInt | None = None
    output: NonNegativeInt | None = None
