# slate

A work tracker built for coding agents. Ideas hold the work nobody has taken
on, with their priority and evidence, until promotion makes each an
initiative. Issues carry their dependencies, labels, comments and the
initiative they ship toward. `slate next` is
the ready queue, ordered by priority. A claim is a lease on a record that
expires, so two agents never hold the same issue and a dead one never holds it
for long. Every claim is kept after it ends, as the record's history.

Two parts share this repository:

- **The API**, in `src/slate/`: FastAPI over PostgreSQL, with Alembic
  migrations. It trusts bearer tokens from one OpenID Connect provider.
- **The CLI**, in `cli/`: a Go client over the API, `slate <noun> <verb>`, with
  `--json` on every read and write. A write prints everything it did beyond
  the request, and each warning it went through despite.

`docs/issues.md` explains the model: what makes an issue ready, how the queue
is ordered and why some things are not built. `docs/claims.md` explains
claims, `docs/worklogs.md` worklogs, `docs/ideas.md` ideas, and
`docs/index.md` lists every page.

## Running the API

```bash
OIDC_ISSUER=https://auth.example.com docker compose up --build
```

That starts Postgres, runs the migrations to head and serves the API at
`http://localhost:8000`. `/health` answers while the process is up, and
`/ready` while Postgres answers too. The API's routes are under `/api/v1`, and
its OpenAPI page is `/api/v1/docs`.

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

### The image

The `Dockerfile` builds the API's image, the same one `docker compose` runs.

- It runs as uid and gid 10400, and owns none of its own files, so its root
  filesystem can mount read-only.
- Migrations are a separate step in the same image: `alembic upgrade head`. Run
  it before the API starts, as the compose file's `migrate` service does.
  Each migration stays compatible with the API one release behind, so the two
  can overlap during a rollout.
- SIGTERM stops new connections and drains the ones in flight for up to ten
  seconds before the process exits.
- Logs go to stdout. Each request is one line carrying its `X-Request-ID`, and
  `/health` and `/ready` are not logged.

### On Kubernetes

Each API release pushes `deploy/` to
`oci://ghcr.io/datapointchris/slate/manifests:<version>`, with the image named
by digest. It holds two kustomize paths:

- `migrate/` runs `alembic upgrade head` as a Job.
- `app/` runs the API as a Deployment behind the Service `slate-api`.

Apply `migrate/`, wait for its Job to complete, then apply `app/`. With Flux,
that is one `OCIRepository` and two `Kustomization`s. The migration's sets
`wait: true`, so it is Ready only once its Job completes, and the app's names
it in `dependsOn`. The Job carries Flux's force annotation, so a new release
replaces it. Applied without Flux, delete the finished Job first.

The cluster supplies what the base leaves out:

- A ConfigMap and a Secret, both named `slate-settings`, holding the settings
  above. `POSTGRES_PASSWORD` goes in the Secret.
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

`slate auth login https://slate.example.com` reads the API's RFC 9728 metadata
for the provider, logs in with the device authorization grant as
`slate-cli-<short hostname>`, and writes `~/.config/slate/config.toml`. The
token is kept in the OS keychain.

A service sets `SLATE_CLIENT_SECRET` and names its own client in the config
file. It then uses the client-credentials grant and stores nothing. A service
reaches only the routes its scopes list:

| Scope | Reaches |
| --- | --- |
| `slate.records.read` | reading any record by number, searching every kind, listing and showing issues, initiatives and ideas, the ready queue, who holds which claim, and worklogs |
| `slate.claims.hold` | holding a claim on a record or on the head of the ready queue, and extending, releasing, waiting on and resuming one, under the service's own client id |
| `slate.issues.close` | commenting on an issue, and closing an open one |
| `slate.records.plan` | reading the vocabulary, filing into triage, setting an issue's priority, initiative and rank, and creating and editing initiatives |
| `slate.worklogs.write` | starting and linking a worklog under the service's own client id, and joining, editing and finishing one it opened |

No scope deletes anything, and none files or changes an idea.
`src/slate/api/client_scopes.py` holds the exact routes.

The CLI requests every scope in the table, so register each on every service
client. A provider that finds one missing may refuse the token outright.

`--repo` takes a repository name. Where a registry of names exists, the CLI
refuses one it does not hold, so a typo cannot file work under a repo nobody
reads. It reads `$SLATE_REPOS_REGISTRY`, then `repos_registry` in
`~/.config/slate/config.yml`, then `~/.local/share/slate/repos.json`. With no
registry, any name passes.

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
