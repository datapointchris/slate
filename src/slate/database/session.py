import functools
from collections.abc import Generator
from contextlib import contextmanager

from sqlalchemy import create_engine
from sqlalchemy.engine import Engine
from sqlalchemy.orm import Session
from sqlalchemy.orm import sessionmaker

from slate.config import Settings
from slate.config import get_settings


@functools.lru_cache(maxsize=4)
def cached_engine(db_uri: str, echo: bool) -> Engine:
    return create_engine(
        db_uri,
        echo=echo,
        pool_pre_ping=True,
        pool_recycle=5,
        connect_args={
            'keepalives': 1,
            'keepalives_idle': 30,
            'keepalives_interval': 10,
            'keepalives_count': 5,
        },
    )


def get_db_engine(settings: Settings) -> Engine:
    return cached_engine(settings.postgres.db_uri, settings.postgres.echo)


@contextmanager
def create_session(settings: Settings):
    """Get the SQLAlchemy session factory configured for the application."""
    SessionLocal = sessionmaker(bind=get_db_engine(settings), autoflush=False)
    session = SessionLocal()
    try:
        yield session
    finally:
        session.close()


def get_sqlalchemy_session() -> Generator[Session]:
    """Return `sqlalchemy.orm.Session` with automatic cleanup.

    Use this for FastAPI dependency injection:

    >>> @router.get('/{id}/')
    >>> def read_label(id: int, session: Session = Depends(get_sqlalchemy_session)):
    >>>     return session.get(models.IssueLabel, id)
    """
    with create_session(get_settings()) as session:
        yield session
