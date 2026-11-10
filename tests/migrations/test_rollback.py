from sqlalchemy import text

from slate.database.migrate import upgrade_to_head

# A revision no migration in this tree holds, as a later release's would be.
LATER_RELEASE_REVISION = 'a1b2c3d4e5f6'


def test_an_earlier_release_leaves_a_database_a_later_one_migrated(scratch_database):
    config, engine = scratch_database
    with engine.begin() as connection:
        connection.execute(text('CREATE TABLE alembic_version (version_num varchar(32) PRIMARY KEY)'))
        connection.execute(text('INSERT INTO alembic_version VALUES (:revision)'), {'revision': LATER_RELEASE_REVISION})

    upgrade_to_head(config)

    with engine.connect() as connection:
        assert connection.scalar(text('SELECT version_num FROM alembic_version')) == LATER_RELEASE_REVISION
