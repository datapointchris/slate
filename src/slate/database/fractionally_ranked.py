import datetime as dt
from uuid import UUID
from uuid import uuid7

from sqlalchemy import DateTime
from sqlalchemy import Double
from sqlalchemy import Uuid
from sqlalchemy import text
from sqlalchemy.orm import Mapped
from sqlalchemy.orm import mapped_column


class FractionallyRanked:
    """The columns `services/fractional_rank.py` orders a table by.

    `created_ts` and then `id` break a tie in `rank` when the table is renumbered.
    """

    id: Mapped[UUID] = mapped_column(Uuid, primary_key=True, default=uuid7)
    rank: Mapped[float] = mapped_column(Double, nullable=False)
    created_ts: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), nullable=False, server_default=text('now()'))
