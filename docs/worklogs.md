# Worklogs

A worklog records one go at one record: the agent sessions that worked it and
when, the commits, pull requests and landings it produced, how the work was
approached, and how the go ended. `slate worklogs` is the CLI, and the API
serves it under `/api/v1/worklogs`.

## A worklog is named for its record and its place among the record's goes

`1991.2` is the second go at record #1991. The sequence counts per record from
one, so the name says what was worked and how many times before. The API takes
the name wherever it takes a worklog: `GET /worklogs/1991.2`.

A worklog is a row of `worklogs` on a record number, like a claim. Its spans
are rows of `worklog_spans`, and its links rows of `worklog_links`.

## A hold opens one, and the claim's ending ends it

Two things open a worklog:

- **A hold.** Each claim opens its own worklog, and the hold's answer names it
  as `worklog_opened`. Every ending of the claim ends the worklog at the same
  moment with the same outcome. [claims.md](claims.md) covers those endings.
- **`slate worklogs start <record>`**, for a go nobody claims, such as
  discussing an idea or planning an initiative. Each worklog already open on
  the record goes through with an `open_worklog` warning, since joining it may
  be what was meant.

A worklog's `outcome` says which ending it reached:

| Outcome | Means |
| --- | --- |
| `finished` | `slate worklogs finish` ended it by hand |
| `released` | its claim was released |
| `expired` | its claim's lease ran out, and it ended at the claim's `expires_ts` |
| `completed`, `canceled`, `dropped` | the record reached that closing status |
| `promoted` | the idea became its initiative |

Closing or promoting a record also ends each worklog `start` opened on it.
Finishing a worklog whose claim still holds goes through with a
`claim_still_held` warning, and the claim keeps holding. Finishing one that
already ended changes nothing and warns `worklog_already_ended`.

A worklog's `state` is derived on every read, like a claim's:

| State | Means |
| --- | --- |
| `open` | unended, and its claim, if it has one, is live |
| `expired` | unended, and its claim ran out; the holder extending the claim opens it again, and any other write that finds the claim ends both |
| `ended` | `ended_ts` is set |

## A session is in one worklog at a time

A span says an agent session was in a worklog from `started_ts` to `ended_ts`.
A partial unique index allows one open span per session. Putting a session in
a worklog ends its open span anywhere else, and the answer names that as
`session_left_worklog`. The worklog it left stays open under its own claim.

So a session holding a claim on one issue, then another, leaves two worklogs.
Each lists the session with its own span, and the spans never overlap. The
second hold moved the session. `slate worklogs join <name>` moves it back.

Time a session spends outside any span belongs to the session alone. So
anything measured over a session, such as its tokens, splits at its span
boundaries with each moment counted once.

This is the single running timer that time trackers such as Toggl Track and
Harvest use: starting one timer stops the one running.

- *Rejected:* a span in every worklog whose claim the session holds. The spans
  would overlap, so a token spent while holding two claims would need an
  arbitrary split or count twice.
- *Rejected:* refusing a second hold while the session is in a worklog. A
  session that parks one claim while it waits on a merge holds the next.

A span in a worklog whose claim ran out ends at the claim's `expires_ts`, not
when a later write noticed.

## A commit reaches its worklog through two git hooks that call slate alone

`slate worklogs current` prints the name of the worklog the session is in, or
nothing at all, and exits 0 either way. A `commit-msg` hook stamps the commit
with it as a trailer:

```sh
#!/bin/sh
name=$(slate worklogs current) && [ -n "$name" ] &&
  git interpret-trailers --in-place --trailer "Worklog: $name" "$1"
exit 0
```

A `post-commit` hook records the commit on the worklog. `slate worklogs link`
with no worklog named links to the session's current one:

```sh
#!/bin/sh
slate worklogs link --commit "$(git rev-parse --short HEAD)" >/dev/null
```

Both read the session from `CLAUDE_CODE_SESSION_ID`, or `--session`. With no
session, `current` prints nothing without asking the API. The trailer is the
commit's own record of its worklog, readable with `git log` and no slate. The
link is slate's record of the commit, readable with no clone. Gerrit's
`Change-Id` hook stamps a commit the same way.

## Links record what a worklog produced, and are never followed

A link is a `commit`, a `pull_request` or a `landing`, with the ref as given
and the repository it is in. slate stores the ref and never resolves it. A
worklog that ended still takes a link, since a pull request lands after the
claim that made it is done. Linking the same ref twice changes nothing and
warns `already_linked`.

A worklog's `stage` is the furthest its links reached, derived on every read:
`started`, `committed`, `pull_request`, then `landed`.

Its `workflow` says how the work was approached: `oneshot`, `plan-first` or
`design-first`. `start --workflow` or `slate worklogs edit --workflow` sets
it, and it may stay unset.

## A service writes worklogs under its own client id

`slate.records.read` reaches every worklog read. `slate.worklogs.write`
reaches starting, joining, linking, editing and finishing. A scoped client
starts and links only as its own client id, and finishes only a worklog that
id opened. A hold's worklog comes with the hold, under `slate.claims.hold`.

Deleting a record deletes its worklogs with their spans and links, and the
answer names them as `worklogs_deleted`.
