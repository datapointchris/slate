# Claims

A claim is a lease on one record of any kind. It says who is working the
record and until when. `slate claims` is the CLI, and the API serves it under
`/api/v1/claims`.

## A claim is a row of its own, kept after it ends

Each claim is a row in `claims` on a record number, so an issue and an
initiative are claimed the same way. A row carries:

| Column | What it says |
| --- | --- |
| `holder` | who holds it, signed the way a close is signed |
| `session`, `host`, `directory` | where the holder works, as the CLI found it |
| `started_ts`, `expires_ts` | when the lease began, and when it runs out |
| `ended_ts`, `ending`, `ended_by` | when and how it stopped, and who stopped it |

`ending` is one of `released`, `expired`, `completed`, `canceled` and
`dropped`. `ended_ts` and `ending` are set together or not at all.

A claim holds no work, only the fact that someone is doing it. Whether an issue
is open or closed is its status. Whether anyone is working it is its claim. So
an issue has no `in_progress` status.

Ending a claim keeps its row. A record's claims are then its history of who
worked it. `GET /issues/{n}` answers them as `claims`, oldest first, and an
issue's `claim` is the one not yet ended.

## Only another holder's live claim refuses a hold

A partial unique index allows one unended claim per record. Two holds racing on
one record are settled by that index: the loser's insert fails, and it answers
409 naming the holder that won. `POST /claims/next` holds the head of the issue
ready queue this way, moving to the next issue when it loses one.

That 409 is the only refusal. A hold goes through whatever else it finds, and
the answer carries a warning for each thing:

| Code | When |
| --- | --- |
| `already_held` | the holder already holds the record; nothing changed |
| `record_closed` | the record is closed |
| `in_triage` | the issue waits on triage |
| `deferred` | the issue's deferral day has not come |
| `shelved` | the initiative is someday, or the issue is shelved in one |
| `open_dependency` | the issue depends on an unclosed issue, one warning each |
| `open_children` | the issue has open children |
| `claimed_initiative` | another holder claims the issue's initiative |
| `claimed_parent`, `claimed_child`, `claimed_dependent` | another holder claims an issue beside it |
| `claimed_issue_in_initiative` | another holder claims an issue in the initiative held |

A neighbor the same holder claims is not named, because a lead holding an
initiative and its issues is one actor working one plan.

Triage, closed, blocked and deferred work are warnings rather than refusals.
Each is a judgment the holder can make with the fact in hand. A second holder
is not: two sessions editing one record collide.

## Nothing extends or ends a claim silently

Holding a record already held under the same name changes nothing, and warns
`already_held`. Only `POST /claims/{record}/extend` moves an expiry. A hold that
quietly extended would hide a second session running under the same name.

A write that ends a claim names it in `side_effects`:

- `claim_ended` when a release ends it, or when completing or canceling an
  issue, or completing or dropping an initiative, ends it. The ending is the
  status reached, and `ended_by` is who closed the record.
- `expired_claim_ended` when a hold finds another holder's claim run out and
  ends it before starting its own.

Releasing a record nobody holds changes nothing and warns `nothing_to_release`.
A person may release another holder's claim, to free work a dead session left,
and the answer warns `released_anothers_claim`.

## A claim that runs out frees its record at once

A claim's `state` is derived on every read, never stored:

| State | Means |
| --- | --- |
| `live` | unended, and its expiry is still ahead |
| `expired` | unended, and its expiry has passed |
| `ended` | ended, by any of the endings above |

Readiness reads only live claims, so an issue whose holder died reads as ready
the moment the lease runs out. The next write that finds the claim ends it as
`expired`, at its `expires_ts`, with no `ended_by`. History then says when
holding stopped, rather than when someone noticed.

The holder extending its own expired claim holds it again from now, and the
answer warns `claim_had_expired`.

## A service claims under its own client id

`slate.claims.hold` reaches the hold, extend and release routes. A scoped client
holds, extends and releases only under its own client id, because its token
says which service it is and nothing says which of its workers acted. It
releases only a claim that id holds.
