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

`session` references `agent_sessions`, the table of every agent session slate
has seen. A hold names it from `CLAUDE_CODE_SESSION_ID` when the CLI runs under
one, and the same row joins the session to its worklogs.

`ending` is a row of `claim_endings`. `released` is a holder or a person
letting go, `expired` is the lease running out first, and `promoted` is an idea
becoming its initiative. The rest name the closing status the record reached.
`ended_ts` and `ending` are set together or not at all.

A claim holds no work, only the fact that someone is doing it. Whether an issue
is open or closed is its status. Whether anyone is working it is its claim. So
an issue has no `in_progress` status.

Ending a claim keeps its row. A record's claims are then its history of who
worked it. `GET /issues/{n}` answers them as `claims`, oldest first, and an
issue's `claim` is the one not yet ended.

## A hold opens a worklog, and the claim's ending ends it

Each hold opens a worklog on the record, named `<record>.<sequence>`, and the
answer names it as `worklog_opened`. The holder's session joins it. A claim's
`worklog` field carries the name. Every way a claim ends also ends its
worklog, at the same moment and with the same outcome, and the answer names it
as `worklog_ended`. An expired claim's worklog ends at the claim's
`expires_ts`. [worklogs.md](worklogs.md) covers the worklog itself.

## A waiting claim still holds its record

A holder parks its claim with `slate claims wait <record> --on <what>`, and
`slate claims resume <record>` clears it. Each wait is a row of `claim_waits`:
what the claim waits on, and when the wait began and ended. A claim answers its
open wait as `waiting`, and `slate claims list` prints it under the claim until
it clears.

Waiting changes nothing about holding. Readiness still reads the claim as live,
and a second holder is still refused. So a session starting cold tells parked
work from live work by `waiting`, never by a missing claim.

Only the holder waits or resumes. A record holding no claim, or another
holder's claim, answers 409, and so does waiting on a claim that ran out.
Waiting on something new ends the wait before it and names it as `wait_ended`.
Waiting on the same thing again warns `already_waiting`. Resuming a claim that
waits on nothing warns `not_waiting`. Ending the claim ends its open wait with
it.

Resuming puts the holder's session back in the claim's worklog, and so does
holding the record again. A session parks one claim and holds the next, so its
commits go to the second. Coming back sends them to the first again.

## Only another holder's live claim refuses a hold

A partial unique index allows one unended claim per record. Two holds racing on
one record are settled by that index: the loser's insert fails, and it answers
409 naming the holder that won. `POST /claims/next` holds the head of the ready
issues this way, moving to the next issue when it loses one.

That 409 is the only refusal. A hold goes through whatever else it finds, and
the answer carries a warning for each thing:

| Code | When |
| --- | --- |
| `already_held` | the holder already holds the record; the claim stays as it was |
| `record_closed` | the record is closed |
| `in_triage` | the issue waits on triage |
| `deferred` | the issue's deferral day has not come |
| `shelved` | the initiative is someday, or the issue is shelved in one |
| `open_dependency` | the issue depends on an unclosed issue, one warning each |
| `open_children` | the issue has open children |
| `idea_promoted` | the idea was promoted; the warning names its initiative |
| `claimed_initiative` | another holder claims the issue's initiative, an initiative whose plan holds the issue, or the initiative an idea became |
| `claimed_parent`, `claimed_child`, `claimed_dependent` | another holder claims an issue beside it |
| `claimed_issue_in_initiative` | another holder claims an issue in the plan of the initiative held |
| `claimed_overlapping_paths` | another holder claims an issue on the same repo whose paths meet this issue's, one warning each |

`WriteEffectCode` in `services/write_effects.py` holds every code, the hold
warnings under their own comment.

A neighbor the same holder claims is not named, because a lead holding an
initiative and its issues is one actor working one plan.

Triage, closed, blocked and deferred work are warnings rather than refusals.
Each is a judgment the holder can make with the fact in hand. A second holder
is not: two sessions editing one record collide.

## A live claim on an initiative takes its plan out of the queue

A coordinator holds the initiative it runs, and its workers hold the issues. So
`GET /queue` lists each ready issue to build in that initiative's plan under
`claimed`, with the initiative's holder as `holder` and the initiative as
`held_through`. The plan is what `for_record` reads: the initiative's open
issues and every open issue they wait on, in any initiative. Two plans sharing
an issue leave it with the earlier claim. `POST /claims/next` passes over the
same issues, unless the caller holds the initiative. Where that leaves it nothing
to hold, it warns `passed_over_plan`, naming each initiative, its holder and the
issues, in place of `nothing_ready`.

A row held this way has no claim of its own, so `holder` holds the initiative and
cannot extend or release the issue.

This is multiple-granularity locking, with the initiative claim as the
intention lock over the issue claims, except that it refuses nothing:

- **An explicit hold goes through and warns `claimed_initiative`,** because each
  of the coordinator's workers holds its issue under its own session. The warning
  names the plan's initiative, and the issue's own initiative too where another
  holder claims that.
- **Readiness ignores the claim,** so the issue reads ready and a worker's queued
  stage is not held by its coordinator's claim.
- **`for_record` keeps the plan in `queue`,** because a coordinator reads its own
  plan there, and `GET /initiatives/progress` names the next issues of the plan.
- **A decision stays in `queue`,** because it waits on a person whoever
  coordinates.

A dead coordinator's plan comes back when its lease runs out, so a coordinator
extends the claim as it works.

## Nothing extends or ends a claim silently

Holding a record already held under the same name leaves the claim as it was,
and warns `already_held`. Only `POST /claims/{record}/extend` moves an expiry. A hold that
quietly extended would hide a second session running under the same name.

A write that ends a claim beyond the one it was asked about names it in
`side_effects`:

- `claim_ended` when completing or canceling an issue, completing or dropping
  an initiative, or promoting an idea ends its claim. The ending is the status
  reached, or `promoted`, and `ended_by` is who closed or promoted the record.
  A release names it only when it finds the claim had already run out, and
  ends it as `expired` rather than `released`.
- `expired_claim_ended` when a hold finds another holder's claim run out and
  ends it before starting its own.
- `worklog_ended` for the worklog of every claim a write ends, a plain
  release included.
- `session_left_worklog` when a hold or a resume moves the holder's session out
  of the worklog it was in. That worklog keeps its state, and its claim, if it
  has one, keeps holding.
- `session_rejoined_worklog` when a resume, or a hold of a record the holder
  already holds, puts the session back in the claim's worklog.

Deleting an issue or an initiative deletes every claim it has had, live or
ended. The answer names them as `claims_deleted`, and warns
`deleted_anothers_claim` when another holder's live claim was one.

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

`slate.claims.hold` reaches the hold, extend, release, wait and resume routes,
and the hold of the ready queue's head. A scoped client acts on claims only
under its own client id, because its token says which service it is and
nothing says which of its workers acted. It releases only a claim that id
holds. `slate.records.read` reaches the claims list, so a service finds what it
holds with `slate claims list --holder <client id>`.
