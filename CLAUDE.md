# CLAUDE.md

slate is a work tracker built for coding agents: a FastAPI API over PostgreSQL in
`src/slate/`, and a Go CLI over that API in `cli/`. `README.md` covers running
it and its settings. `docs/index.md` lists the design pages, and
`docs/issues.md` is the model: readiness, order, claims and closing.

## Layout

- `src/slate/api/` — the app (`main.py`), auth (`oidc_auth.py`, `callers.py`,
  `client_scopes.py`) and one router per resource in `endpoints/`
- `src/slate/models/`, `schemas/`, `services/`, `database/` — SQLAlchemy 2
  models, Pydantic schemas, the derivations reads share, and the session
- `src/slate/alembic/` — migrations; `alembic.ini` finds them as
  `slate:alembic`, so the installed package carries them
- `cli/` — its own Go module, `github.com/datapointchris/slate/cli`, built with
  cobra. `internal/api` is the typed client and `internal/cli` the commands
- `tests/` — pytest, against a Postgres it starts itself

## Commands

```bash
uv run pytest                      # needs Docker: starts a throwaway Postgres
cd cli && go test ./...
OIDC_ISSUER=https://auth.example.com docker compose up --build
```

## Auth has one strategy: an OIDC bearer token

Every request carries an RFC 9068 access token from the issuer `OIDC_ISSUER`
names. The client id's prefix decides who is calling:

- `slate-cli-` is a person through the CLI's device login. The caller is an
  `OIDCIdentity`, and its `subject` signs what it changes.
- `slate-svc-` is a service through the client-credentials grant. The caller
  is a `ScopedClient`, and reaches only the routes `client_scopes.py` lists for
  its scopes.

There are no users and no sessions. Routes take `CurrentCaller` from
`api/callers.py`, and tests override `get_current_caller`.

**A new router goes in `RESOURCE_ROUTERS` in `api/main.py`.** FastAPI hands a
dependency the router-local path (`/{id}`), not the full template, so
`create_api` builds a `(method, endpoint) → template` table from that list. The
scope gate reads it. A router left out answers every scoped client with 403.

`tests/api/test_client_scopes.py` writes `tests/api/testdata/client-scopes.json`,
and the CLI's service tests read it to check each call a service makes lands on
a route its scopes reach. Commit the file when the scopes change.

## Reads derive, writes are audited

- **Every `is_*` flag on an issue, and its effective priority, is derived on
  every read** in `services/issue_readiness.py`. A client filters on those
  fields and never re-derives them.
- **Every write is audited in `audit_trail`** by a row trigger on each table,
  so no write path does anything for it. A new table's migration creates its
  trigger, and `test_every_table_is_audited_but_what_opts_out` names the
  statement when one is missing. The actor is the caller's subject or client
  id, set per transaction by `bind_actor`.
- **A read answering with a list takes `RowLimit` and `apply_row_limit`** from
  `services/row_limit.py`. A hand-declared `limit: int | None` lets a negative
  reach SQL and reads `limit=0` as every row.
- **A handler is `def` unless it awaits something.** The session is
  synchronous, and an `async def` handler calling it holds every other request
  on the event loop.

## Schema rules

- **Every `*Update` field may be omitted.** A field over a NOT NULL column is
  `NotNull[T] = None`, so an explicit null answers 422 rather than reaching the
  column as a 500. `test_update_schemas_refuse_null.py` walks every schema.
- **A time column is one of two kinds.** A moment that happened is
  `DateTime(timezone=True)` and its request field is `AwareDatetime`. A calendar
  day is `Date`, its request field is `date`, and it never travels as a
  timestamp. A bounded read takes `since` and `until` through `TimeWindowQuery`,
  and reads a `Date` column in the `timezone` query parameter, else UTC.
- **The CLI decodes a day as a `string`, never a `time.Time`.** `time.Time`
  refuses a bare day, so one dated row would fail a whole list.

## Migrations

- **A revision comes from `uv run alembic revision --autogenerate`,** never a
  hand-written id. Autogenerate does not see sequences, rows in lookup tables
  or triggers, so a migration adds those by hand, as the first one does.
- **A migration keeps the API one release behind working.** The API rolls out
  beside the release before it, so a column is added before code reads it and
  dropped only after no running code does. A type change goes through a new
  column.
- `uv run alembic check` after a model change reports any drift between the
  models and the migrations.

## Logging

structlog writes one JSON object per line to stdout, through the stdlib root
logger, so uvicorn's and alembic's lines come out the same way.
`RequestLoggingMiddleware` logs each request once, under its `X-Request-ID`.
A client's 4xx logs at warning and only a 5xx at error.
