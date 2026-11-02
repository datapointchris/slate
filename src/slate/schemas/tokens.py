"""Tokens counted by kind, the one shape every read and write of usage carries."""

from pydantic import BaseModel
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
