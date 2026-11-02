from fastapi import HTTPException
from fastapi import status
from sqlalchemy import select
from sqlalchemy.orm import Session


def validate_lookup(session: Session, model, value: str, noun: str, extra: tuple[str, ...] = ()) -> None:
    """Reject an unknown vocabulary value by name rather than letting the foreign key raise."""
    if value in extra or session.get(model, value) is not None:
        return
    known = [*session.scalars(select(model.name).order_by(model.name)).all(), *extra]
    raise HTTPException(
        status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
        detail=f'Unknown {noun} {value!r}. Known: {", ".join(known)}',
    )
