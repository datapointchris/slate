"""An explicit null in a PATCH body never reaches a NOT NULL column.

Each `*Update` schema is walked against the model its name points at, so a
new schema or a new NOT NULL column is covered without anyone listing it here.
A null that got through would come back from the database as a 500.
"""

import inspect
from collections.abc import Iterator

import pytest
from pydantic import BaseModel
from pydantic import ValidationError

from slate import models
from slate import schemas

UPDATE_SCHEMAS = {
    name: cls for name, cls in inspect.getmembers(schemas, inspect.isclass) if name.endswith('Update') and issubclass(cls, BaseModel)
}


def model_for(schema_name: str):
    return getattr(models, schema_name.removesuffix('Update'), None)


def not_null_fields(schema: type[BaseModel]) -> list[str]:
    return [
        attr.key
        for attr in model_for(schema.__name__).__mapper__.column_attrs
        if attr.key in schema.model_fields and not attr.columns[0].nullable
    ]


def not_null_cases() -> Iterator[tuple[str, str]]:
    for name, schema in sorted(UPDATE_SCHEMAS.items()):
        for field in not_null_fields(schema):
            yield name, field


CASES = list(not_null_cases())


def test_every_update_schema_names_its_model():
    assert len(UPDATE_SCHEMAS) >= 3, 'the walk found too few schemas to be reading the package'
    unmatched = sorted(name for name in UPDATE_SCHEMAS if model_for(name) is None)
    assert not unmatched, f'no model named after {unmatched}'
    assert len(CASES) >= 10, 'too few NOT NULL fields to be reading the models'


@pytest.mark.parametrize(('schema_name', 'field'), CASES)
def test_an_explicit_null_is_refused_or_normalized(schema_name, field):
    schema = UPDATE_SCHEMAS[schema_name]
    try:
        sent = schema.model_validate({field: None}).model_dump(exclude_unset=True)
    except ValidationError as refusal:
        assert field in str(refusal), 'the refusal names the field the caller has to fix'
    else:
        assert sent.get(field, ...) is not None, f'{schema_name} hands a null for NOT NULL {field} to the database'
