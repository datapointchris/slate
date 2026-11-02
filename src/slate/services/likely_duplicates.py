"""Name the open records a new one reads like, so whoever files it sees the work it may repeat.

Likeness is the Dice coefficient over the stemmed words of two titles: twice the stems they share,
over the stems the two hold. Postgres' English text search config stems and drops stop words, so
`transcripts` meets `transcript` and `the` counts for nothing. `docs/records.md` gives the
measurement behind the bar. A filing goes through whatever it finds, and each match is a warning.
"""

from __future__ import annotations

from dataclasses import dataclass

from sqlalchemy import ColumnElement
from sqlalchemy import Text
from sqlalchemy import func
from sqlalchemy import literal
from sqlalchemy import literal_column
from sqlalchemy import null
from sqlalchemy import select
from sqlalchemy import union_all
from sqlalchemy.dialects.postgresql import ARRAY
from sqlalchemy.dialects.postgresql import REGCONFIG
from sqlalchemy.orm import Session

from slate import models
from slate.models.record import Kind
from slate.services.open_records import is_open
from slate.services.write_effects import WriteEffectCode
from slate.services.write_effects import WriteEffects

DUPLICATE_LIKENESS = 0.45
MOST_NAMED = 3

ENGLISH = literal_column("'english'", REGCONFIG)


def stems(column: ColumnElement[str]) -> ColumnElement[list[str]]:
    return func.tsvector_to_array(func.to_tsvector(ENGLISH, column), type_=ARRAY(Text))


@dataclass(frozen=True)
class OpenRecord:
    number: int
    kind: str
    title: str
    repo: str | None
    stems: frozenset[str]


def dice(a: frozenset[str], b: frozenset[str]) -> float:
    if not a or not b:
        return 0.0
    return 2 * len(a & b) / (len(a) + len(b))


def open_record_titles():
    """Every open record of every kind, with its title's stems."""
    return union_all(
        select(models.Issue.number, literal(Kind.ISSUE.value), models.Issue.title, models.Issue.repo, stems(models.Issue.title)).where(
            is_open(Kind.ISSUE)
        ),
        select(
            models.Initiative.number, literal(Kind.INITIATIVE.value), models.Initiative.name, null(), stems(models.Initiative.name)
        ).where(is_open(Kind.INITIATIVE)),
        select(models.Idea.number, literal(Kind.IDEA.value), models.Idea.title, null(), stems(models.Idea.title)).where(is_open(Kind.IDEA)),
    )


def likely_duplicates(session: Session, number: int, title: str, repo: str | None) -> list[OpenRecord]:
    """The open records whose titles read most like `title`, most alike first.

    Two records on different repos are different work, however alike their titles read. A record
    on no repo, which every idea and initiative is, is read against every other.
    """
    filed = frozenset(session.scalar(select(stems(literal(title)))) or ())
    scored = []
    for row in session.execute(open_record_titles()).all():
        record = OpenRecord(row[0], row[1], row[2], row[3], frozenset(row[4] or ()))
        if record.number == number or (repo is not None and record.repo is not None and record.repo != repo):
            continue
        if (likeness := dice(filed, record.stems)) >= DUPLICATE_LIKENESS:
            scored.append((likeness, record))
    scored.sort(key=lambda pair: (-pair[0], -pair[1].number))
    return [record for _, record in scored[:MOST_NAMED]]


def warn_likely_duplicates(session: Session, number: int, title: str, repo: str | None, effects: WriteEffects) -> None:
    for record in likely_duplicates(session, number, title, repo):
        effects.warn(
            WriteEffectCode.POSSIBLE_DUPLICATE,
            f'#{record.number} {record.title!r}, an open {record.kind}, reads like #{number}. Check it is not the same work.',
            record.number,
        )
