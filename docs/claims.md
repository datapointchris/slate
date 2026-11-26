# Claims

A claim is a lease on one idea, initiative or issue. A pursuit holds no work,
so no claim holds one. A claim says who is working the record and until when. `slate claims` is the CLI, and the API serves it under
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
letting go, `expired` is the lease running out first, `promoted` is an idea
becoming its initiative, and `adopted` is another session taking over the claim
of one that died. The rest name the closing status that the record reached.
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

The claim moves to a worklog that its holder's session starts or joins on the
record later, so `worklog` names the go under way rather than the one that the
hold opened. A reader matching a run's worklog against the claim's then finds
the step the holder is on.

## A move between steps is one write, and so is taking over a dead session's claim

`slate claims advance <record> --step <step>` moves the holder's claim to
another step of the record's life. In one transaction it ends the worklog that
the claim is on, opens one for `<step>` whose `follows` names the one ended, and
moves the claim and the caller's session into it. An open wait ends, as a
resume ends it. So nothing between two calls leaves the record unclaimed, or
its claim on a step that no worklog is open for.

| Move | The worklog it leaves ends as | Its step |
| --- | --- | --- |
| to a later step, or from a worklog naming none | `finished` | checked |
| to an earlier step | `moved_back` | unchecked, so the step reads as still to do |

`moved_back` is a worklog outcome and never a claim's ending, because the claim
goes on. The holder's own claim that ran out holds again for the request's
`minutes`, as an extend holds it, and the answer warns `claim_had_expired`.

`advance` answers 409 in each case where it would move nothing, naming the
command that would succeed, or the holder:

| Case | The refusal names |
| --- | --- |
| an idea | `slate ideas promote <record>`, which takes an idea to design |
| a closed record | `slate issues reopen` or `slate initiatives reopen` |
| no claim, or another holder's that ran out | `slate claims hold <record> --step <step>` |
| another holder's live claim | the holder and its expiry |
| the claim's open worklog already at `<step>` | `slate claims resume <record>` |

A pursuit holds no work, so `advance` answers it 422.

`slate claims adopt <record> --from <session>` takes over the claim of a
session that died. In one transaction it ends that claim `adopted`, which checks
nothing, and holds the record for the caller with a worklog of the same step,
whose `follows` names the one ended. A claim that ran out has already ended as
`expired` at its expiry, so it keeps that ending, and the caller still takes the
step.

A dead session may have finished its worklog by hand before it died. That step
is then done, and taking it again would move its check to the caller's worklog.
So where the claim's worklog has ended, adopt answers 409 unless `--step` names
the step to take. `--step` also overrides the step of an open worklog.

It answers 409 where the record holds no claim, or where by then a session
other than `--from` holds it, and 422 on a pursuit. Only a person adopts,
because only a person can tell that a session is dead. So no scope reaches the
route.

- *Rejected:* `slate worklogs finish`, `slate claims release` and the next
  hold, run in turn. Nobody holds the record between the release and the
  hold, and a session that dies between a finish and a start leaves a claim
  with no live step worklog.
- *Rejected:* releasing a dead session's claim and holding it again. The
  release checks a step that the dead session never finished.

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

A wait records the worklog that the claim is on as it begins, in
`claim_waits.worklog_id`. A worklog's read lists its own waits by that column.
So a claim that moves across every step leaves each step's waits on that
step's worklog, rather than every wait on the last.

Resuming puts the holder's session back in the claim's worklog, and so does
holding the record again. A session parks one claim and holds the next, so its
commits go to the second. Coming back sends them to the first again.

## Only another holder's live claim refuses a hold

A partial unique index allows one unended claim per record. Two holds racing on
one record are settled by that index: the loser's insert fails, and it answers
409 naming the holder that won. Every write that decides from a record's
unended claim locks its row first, with `SELECT ... FOR UPDATE`. So two such
writes take turns, and the second reads the claim as the first left it. `POST /claims/next` holds the head of the ready
issues this way, moving to the next issue when it loses one.

That 409 is the only refusal of a hold on work. A pursuit holds none, so a
hold on one answers 422 before anything is taken. A hold goes through whatever
else it finds, and the answer carries a warning for each thing:

| Code | When |
| --- | --- |
| `already_held` | the holder already holds the record; the claim stays as it was |
| `record_closed` | the record is closed |
| `incoming` | the issue is incoming, so nobody has accepted it |
| `deferred` | the issue's deferral day has not come |
| `shelved` | the initiative is someday, or the issue is shelved in one |
| `open_dependency` | the issue depends on an unclosed issue, one warning each |
| `initiative_waits` | the issue's open initiative waits on an open initiative or issue, one warning each |
| `open_children` | the issue has open children |
| `claimed_initiative` | another holder claims the issue's initiative, or an initiative whose plan holds the issue |
| `claimed_parent`, `claimed_child`, `claimed_dependent` | another holder claims an issue beside it |
| `claimed_issue_in_initiative` | another holder claims an issue in the plan of the initiative held |
| `claimed_overlapping_paths` | another holder claims an issue on the same repo whose paths meet this issue's, one warning each |

`WriteEffectCode` in `services/write_effects.py` holds every code, the hold
warnings under their own comment.

A neighbor that the same holder claims is not named, because a lead holding an
initiative and its issues is one actor working one plan.

Incoming, closed, blocked and deferred work are warnings rather than refusals.
Each is a judgment that the holder can make with the fact in hand. A second
holder is not: two sessions editing one record collide.

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
- `worklog_ended` for the worklog of every claim that a write ends, a plain
  release included.
- `session_left_worklog` when a hold or a resume moves the holder's session out
  of the worklog it was in. That worklog keeps its state, and its claim, if it
  has one, keeps holding.
- `session_rejoined_worklog` when a resume, or a hold of a record that the
  holder already holds, puts the session back in the claim's worklog.
- `claim_moved_to_worklog` when the holder's session starts or joins another
  worklog on the record, or an advance opens one, and the claim moves to it.
- `wait_ended` when an advance ends the claim's open wait.

Deleting an issue, an initiative or an idea deletes every claim it has had,
live or ended. The answer names them as `claims_deleted`, and warns
`deleted_anothers_claim` when another holder's live claim was one.

Releasing a record that nobody holds changes nothing and warns
`nothing_to_release`. A person may release another holder's claim, to free work
a dead session left, and the answer warns `released_anothers_claim`.

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

An extend never moves an expiry earlier. A lease already running past
`--minutes` from now keeps its expiry, and the answer warns
`lease_already_longer`. So a coordinator that extends by a day each round
leaves a week-long lease on a parked claim alone.

## A service claims under its own client id

`slate.claims.hold` reaches the hold, extend, release, wait, resume and
advance routes, and the hold of the ready queue's head. No scope reaches
adopt. A scoped client acts on claims only
under its own client id, because its token says which service it is and
nothing says which of its workers acted. It releases only a claim that id
holds. `slate.records.read` reaches the claims list, so a service finds what it
holds with `slate claims list --holder <client id>`.
