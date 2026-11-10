# slate

A work tracker built for coding agents. Ideas hold the work nobody has taken
on, with their priority and evidence, until promotion makes each an
initiative. Issues carry their dependencies, labels and the initiative they
ship toward. Any record keeps versioned documents, such as its design and
plan, and comments on the record or on one line of a document.
`slate next` lists every kind of open work in the order to take it, each with
its step, and the work someone holds apart. A claim is a lease on a record that
expires, so two agents never hold the same issue and a dead one never holds it
for long. Every claim is kept after it ends, as the record's history. Every
write is audited, so `slate show` prints who changed what on a record, and
when.

Two parts share this repository:

- **The API**, in `src/slate/`: FastAPI over PostgreSQL, with Alembic
  migrations. It trusts bearer tokens from one OpenID Connect provider.
- **The CLI**, in `cli/`: a Go client over the API, `slate <noun> <verb>`, with
  `--json` on every read and write. A write prints everything it did beyond
  the request, and each warning it went through despite.

`docs/issues.md` explains the model: what makes an issue ready, how the queue
is ordered and why some things are not built. `docs/claims.md` explains
claims, `docs/worklogs.md` worklogs, `docs/sessions.md` the tokens each
agent session spent and the Claude Code hook that reports them,
`docs/totals.md` the totals of the work over any record, repo or window,
`docs/ideas.md` ideas, `docs/tracing.md` the trace each request joins, and
`docs/index.md` lists every page.

## Running the API

```bash
OIDC_ISSUER=https://auth.example.com docker compose up --build
```

That starts Postgres, runs the migrations to head and serves the API at
`http://localhost:8000`. `/health` answers while the process is up, and
`/ready` once Postgres has answered once. A later Postgres outage leaves it
ready, and each request answers 503 until Postgres is back. The API's routes
are under `/api/v1`, and its OpenAPI page is `/api/v1/docs`.

### Settings

Every setting is an environment variable. A `.env` file in the working
directory is read first where one exists, and the environment outranks it.

| Variable | Default | What it sets |
| --- | --- | --- |
| `OIDC_ISSUER` | required | The provider whose RFC 9068 access tokens the API accepts |
| `OIDC_CLI_CLIENT_ID_PREFIX` | `slate-cli-` | The client ids a person's CLI logs in as |
| `OIDC_SERVICE_CLIENT_ID_PREFIX` | `slate-svc-` | The client ids a service authenticates as |
| `PUBLIC_URL` | unset | The address clients reach the API at. Set, it serves RFC 9728 metadata naming the issuer |
| `POSTGRES_HOST` | required | |
| `POSTGRES_PORT` | `5432` | |
| `POSTGRES_USERNAME` | required | |
| `POSTGRES_PASSWORD` | required | |
| `POSTGRES_DB` | required | |
| `LOG_FORMAT` | `json` | `json` for one object per line, `console` for a terminal |
| `LOG_LEVEL` | `INFO` | A Python logging level name |
| `OTEL_TRACES_EXPORTER` | unset | The exporters to run, from those `docs/tracing.md` lists. Unset, `otlp` where an OTLP endpoint is set, else nothing exports |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | unset | The OTLP/HTTP receiver spans go to. Every other `OTEL_*` variable the SDK reads applies too |

Each request continues the caller's `traceparent`, and its span names the
record its route is about. `docs/tracing.md` says what each span carries.

### The image

The `Dockerfile` builds the API's image, the same one `docker compose` runs.

- It runs as uid and gid 10400, and owns none of its own files, so its root
  filesystem can mount read-only.
- Migrations are a separate step in the same image:
  `python -m slate.database.migrate`, which runs `alembic upgrade head`. Run
  it before the API starts, as the compose file's `migrate` service does.
  Each migration stays compatible with the API one release behind, so the two
  can overlap during a rollout. For the same reason, the step leaves a
  database a later release migrated as it is and exits 0, so an earlier
  release can be rolled back to.
- SIGTERM stops new connections and drains the ones in flight for up to ten
  seconds before the process exits.
- Logs go to stdout. Each request is one line carrying its `X-Request-ID` and
  its trace, and `/health` and `/ready` are not logged.

### On Kubernetes

Each API release pushes `deploy/` to
`oci://ghcr.io/datapointchris/slate/manifests:<version>`, with the image named
by digest. It holds two kustomize paths:

- `migrate/` runs `python -m slate.database.migrate` as a Job.
- `app/` runs the API as a Deployment behind the Service `slate-api`.

Apply `migrate/`, wait for its Job to complete, then apply `app/`. With Flux,
that is one `OCIRepository` and two `Kustomization`s. The migration's sets
`wait: true`, so it is Ready only once its Job completes, and the app's names
it in `dependsOn`. The Job carries Flux's force annotation, so a new release
replaces it. Applied without Flux, delete the finished Job first.

The cluster supplies what the base leaves out:

- A ConfigMap named `slate-settings`, holding the settings above, all but the
  Postgres login.
- Two Secrets holding the Postgres login as `POSTGRES_HOST`, `POSTGRES_PORT`,
  `POSTGRES_USERNAME`, `POSTGRES_PASSWORD` and `POSTGRES_DB`. The migration
  Job reads `postgres`, whose role owns the schema. The API reads
  `postgres-app`, whose role needs `SELECT`, `INSERT`, `UPDATE` and `DELETE`
  on every table and `USAGE` on every sequence, and no right to change the
  schema. Default privileges from the owner give it the same on what a later
  migration creates.
- A route to `slate-api` reaching `/.well-known/oauth-protected-resource` as
  well as `/api/v1`, at the address `PUBLIC_URL` names.
- Each NetworkPolicy the pods need to reach Postgres and the issuer.

Both pods run as the image's own uid under the restricted Pod Security
profile, on a read-only root filesystem.

The release signs the image and the manifests with Sigstore, as this
repository's release workflow on `main`. Check a version before applying it:

```bash
cosign verify \
  --certificate-identity https://github.com/datapointchris/slate/.github/workflows/release.yml@refs/heads/main \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/datapointchris/slate/manifests:<version>
```

A Flux `OCIRepository` checks the same identity on every revision:

```yaml
verify:
  provider: cosign
  matchOIDCIdentity:
    - issuer: ^https://token\.actions\.githubusercontent\.com$
      subject: ^https://github\.com/datapointchris/slate/\.github/workflows/release\.yml@refs/heads/main$
```

## The CLI

```bash
cd cli && go build -o slate . && ./slate --help
```

`slate tui` opens a live board of the initiatives, with the total, each priority
tier and each initiative's progress on one set of columns. Its repos tab puts each
repo on the same columns, with its closes per week and how long its ready issues
have waited. From the board you move, pin and reorder work, open a Claude session
on a row, and open a pull request an issue's worklogs hold in `prs`.
`docs/live-board.md` says why it has that shape.

`slate auth login https://slate.example.com` reads the API's RFC 9728 metadata
for the provider, logs in with the device authorization grant as
`slate-cli-<short hostname>`, and writes `~/.config/slate/config.toml`. The
token is kept in the OS keychain.

A service sets `SLATE_CLIENT_SECRET` and names its own client in the config
file. It then uses the client-credentials grant and stores nothing. A service
reaches only the routes its scopes list:

| Scope | Reaches |
| --- | --- |
| `slate.records.read` | reading the vocabulary, reading any record by number, searching every kind, listing and showing issues, initiatives and ideas, the queue `slate next` reads, who holds which claim, worklogs, documents, comments, a session's tokens, and the work's totals |
| `slate.claims.hold` | holding a claim on a record or on the head of the ready issues, and extending, releasing, waiting on and resuming one, under the service's own client id |
| `slate.comments.write` | commenting on any record, or on one line of its document, under the service's own client id |
| `slate.documents.write` | writing the next version of a document on any record, under the service's own client id, which keeps every earlier version |
| `slate.issues.close` | closing an open issue |
| `slate.records.plan` | filing into triage, setting an issue's priority, initiative, acceptance and paths, adding and removing what an issue or an initiative waits on, moving an issue, initiative or idea in the queue, and creating and editing initiatives |
| `slate.worklogs.write` | starting and linking a worklog under the service's own client id, and joining, editing and finishing one it opened |
| `slate.worklogs.observe` | joining a session that commits nothing, such as a pull request's reviewer, to a live worklog another client opened |
| `slate.sessions.write` | reporting the tokens an agent session spent, which replaces that session's last report |

No scope deletes anything, and none files an idea or edits one. A planning
job only moves one in the queue.
`src/slate/api/client_scopes.py` holds the exact routes.

Each command asks the grant for the scopes its own routes need, and no others.
Its `--help` ends with them under `Service scopes:`, so register a service
client for the scopes of the commands it runs. A command no scope reaches, such
as `slate labels list`, says so there, and refuses a service before asking.
`slate auth status` asks a grant for each scope in the table alone. Its text
lists the scopes the client holds. `--json` gives each scope's `grant`:

- `granted`, a token carrying it.
- `refused`, the provider refusing it.
- `unknown`, an answer about the client or the provider, or none.

So a wrong secret answers every scope `unknown` with `session` `rejected`. A
client registered for no scope answers every one `refused`. A provider failing
one probe leaves the others' answers standing.

`slate auth token` asks the same, then one grant for every scope found held.
It leaves out a scope the provider gave no answer for, with a warning on stderr.

A provider says a scope is missing in one of two ways, and slate reads both:

- **It refuses the whole request with `invalid_scope`**, as Authelia does. A
  command reports the scopes it asked for. `auth status` answers that scope
  `refused`.
- **It grants a narrower token and names what it granted** in the response's
  `scope`, as RFC 6749 §3.3 and §5.1 allow. A command refuses the token before
  calling the API, naming the scopes left out. `auth status` answers each one
  left out `refused`.

A response naming no `scope` granted every scope asked, as §5.1 says.

`--repo` takes a repository name. Where a registry of names exists, the CLI
refuses one it does not hold, so a typo cannot file work under a repo nobody
reads. It reads `$SLATE_REPOS_REGISTRY`, then `repos_registry` in
`~/.config/slate/config.yml`, then `~/.local/share/slate/repos.json`. With no
registry, any name passes. An entry's `path` names the repo's checkout, where
`C` in `slate tui` opens a Claude session on that repo's work.

The exit status says who has to act, so a program calling slate never reads the
message:

| Status | Means |
| --- | --- |
| 0 | it worked |
| 1 | slate answered and refused, or the record is not there |
| 2 | the command line was wrong |
| 4 | no whole answer came: unreachable, timed out, cut short, or a 429 or 5xx, so the same command run later can succeed |
| 5 | a login, token or scope stopped the request, so a person has to fix the credential |

## Releases

A push to `main` releases what its conventional commits earned, in two
streams that move apart:

- **The CLI, tagged `cli/vX.Y.Z`,** when a commit touched `cli/`. Its GitHub
  release carries a `slate` archive per platform, and `slate update` replaces
  a released binary with the newest.
- **The API, tagged `vX.Y.Z`,** when a commit touched what the image is built
  from or `deploy/`. It publishes `ghcr.io/datapointchris/slate:<version>` and
  the manifests above.

Both stay on 0.x, so a breaking change bumps the minor version.

## Development

```bash
task setup
task test     # pytest starts a throwaway Postgres in Docker
task lint
```

`task` lists the rest.

## License

MIT
