from alembic import command
from alembic.script import ScriptDirectory
from sqlalchemy import Connection
from sqlalchemy import text

# Every migration after this one keeps the release before it working. This one renamed
# audit_trail.changed_at in place while the release before it still read the column.
CHECKED_AFTER = 'e2eebda09b7b'

# A table, or a table.column, a migration removes once the release before it reads it no more, by
# revision. One goes here only after a `git grep` on the release before that migration finds no reader.
REMOVED_ONCE_UNREAD: dict[str, set[str]] = {}


def tables_and_columns(connection: Connection) -> set[str]:
    rows = connection.execute(
        text(
            'SELECT table_name, column_name FROM information_schema.columns'
            " WHERE table_schema = 'public' AND table_name <> 'alembic_version'"
        )
    ).all()
    return {table for table, _ in rows} | {f'{table}.{column}' for table, column in rows}


def removed(before: set[str], after: set[str]) -> set[str]:
    """What a step removed, naming a dropped table alone rather than each of its columns too."""
    gone = before - after
    return {name for name in gone if '.' not in name or name.split('.')[0] not in gone}


def test_no_migration_removes_what_the_release_before_reads(scratch_database):
    config, engine = scratch_database
    steps = [revision.revision for revision in ScriptDirectory.from_config(config).walk_revisions(CHECKED_AFTER, 'heads')]
    steps = steps[::-1][1:]
    command.upgrade(config, CHECKED_AFTER)
    with engine.connect() as connection:
        schema = tables_and_columns(connection)

    removals = {}
    for revision in steps:
        command.upgrade(config, revision)
        with engine.connect() as connection:
            after = tables_and_columns(connection)
        if found := removed(schema, after) - REMOVED_ONCE_UNREAD.get(revision, set()):
            removals[revision] = sorted(found)
        schema = after

    assert removals == {}, (
        'A migration removes what the release before it may still read, which fails that release while both serve. '
        'Add the replacement in one release and remove the old one in a later release, listing it in REMOVED_ONCE_UNREAD.'
    )
