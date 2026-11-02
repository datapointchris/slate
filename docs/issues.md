# Issues

An issue is a unit of software work, written for the agent that will do it.
`slate issues` is the CLI, and the API serves it under `/api/v1/issues/`.

## An issue's number is shared with every other kind of record

An issue's `number` is its row in `records`, which ideas and initiatives draw
from too. So a bare `#42` names exactly one record, and a commit message or a
conversation can cite it without saying which kind it is.
[records.md](records.md) explains the table.

`id` is the key. `number` is the handle a person or an agent types.

## The ready queue is derived on every read, never stored

An issue is ready when all of these hold:

- it is `open`, and nobody holds a live claim on it
- its not-before day, `deferred_until_date`, has arrived in the reader's calendar
- every issue it depends on is closed
- it has no open children
- it is not shelved: in a `someday` initiative with nothing outside someday
  depending on it

Every input to that test changes somewhere else. A blocker closes, a claim
expires, a day arrives, a child closes. A stored flag would be wrong the
moment any of them moved, so `services/issue_readiness.py` derives readiness
for every issue in one pass on each read. The API returns it as `is_ready`
and `is_blocked`, and every client filters on those rather than re-deriving
them.

`GET /queue` lists the ready issues among every other kind of open work, in
the order to take it. `slate next` prints it.

## `next` lists every kind of open work, each row with the step it asks for

A row is a record and what taking it means:

| Step | Row |
| --- | --- |
| `discuss` | an idea nobody has promoted |
| `plan` | an active initiative with no unclosed issue |
| `decide` | a ready `decision` issue |
| `build` | any other ready issue, or a held initiative whose issues are being built |

`step` keeps one step's rows, so `slate next --step build` is the work an agent
takes. An initiative holding unclosed issues is not a row of its own, because
its issues are. It is listed only when held, in `claimed`, at step `build`, so
whoever holds it is seen beside the issues it gathers. `repo`, `type`, `label` and `autofixable` read only issues, so
each leaves the ideas and initiatives out. `initiative` narrows to one
initiative's rows.

Each row says its `kind`, `number`, `step` and `priority`, and carries the
kind's own read as `record`. `priority` is the one the row sorts at: an
issue's effective priority, else the record's own.

A live claim says someone is working the record, so a held record is listed
apart in `claimed`, by holder, whatever its readiness. The rest are `queue`,
first row next. `limit` caps `queue` and never `claimed`.

`POST /claims/next` holds the head of the `build` rows under the same filters.
One function in `services/work_queue.py` selects and orders the issues for
both, so the preview and the hold cannot disagree.

## A decision waits on a person, so it is decided rather than built

The `decision` type is the one type that changes what happens to an issue.
It needs a person to choose, and an agent that held one could only stall. So
`next` lists a ready decision at step `decide`, and `POST /claims/next`
refuses `type=decision` with 422. A person holds a decision by naming it.

The other types are `bug`, `feature`, `task` and `chore`. They differ only in
what they say about the work, and `task` is the default.

## A dependency means the work cannot start until the other issue closes

An edge from A to B says A cannot start while B is open. It never means
related, or better done after. Work that only touches the same code names the
same paths instead, and the hold warns.

- **A gate on going live sits on the issue that goes live.** The issue that
  deploys depends on its review. The work building it does not, so the work
  starts while the review waits.
- **A question only a person can answer is a `decision` issue the work depends
  on.** The decision then inherits the priority of the work it gates, so
  `next` lists it at step `decide` at that priority. A question written into
  the description instead blocks nothing, and an agent reaching it can only
  stall.
- **The description is the issue's current state.** A correction rewrites it,
  and a comment keeps the history of when and why.

## A someday initiative holds ideas until active work needs one

An initiative is `someday` when it is an idea not yet taken on. Its issues are
captured in full, but the ready queue leaves them out, so no agent takes work
nobody chose. Reopening the initiative makes them ready. Moving one issue into
an active initiative takes that one alone.

An issue outside someday that depends on a someday issue, directly or through a
chain, has chosen it. That someday issue enters the queue at the dependent's
priority and stays in its initiative, because it belongs with that idea while
something urgent needs it first. Closing the dependent shelves it again.

`someday` is neither active nor terminal. It lends no priority, and like a
closed initiative it gives up its name, so an active one may take it.

## `autofix: false` keeps an issue from an unattended fix and leaves it ready for a session

An unattended fix is a commit nobody reads before it lands. Two kinds of work
must stay out of one. A step of a larger design would land alone, out of the
design it belongs to. That design is an initiative, or a parent issue split
into children. A standalone issue may need a person, or someone may simply
want to do it themselves.

Issues and initiatives each carry `autofix`, true by default. An issue is
autofixable when none of it, its initiative and its unclosed parent says false,
and the parent is read the same way up the tree. That is derived on every read
with readiness and returned as `is_autofixable`. So an issue filed later into
such an initiative, or under such a parent, is covered with nothing set on it.

`autofix` says nothing about whether an issue is ready. A session reads the
whole queue with `slate next`. `slate next --step build --autofixable` is the
queue for an unattended fix, and is `GET /queue?step=build&autofixable=true`.
The filter is named for `is_autofixable`, the derived fact it reads, the way
`blocked` reads `is_blocked`. `autofix` stays the stored setting.

The flag is a veto the reader asks to honor. A hold by number takes an issue
whatever its flag says, and a fixer narrowing further, by type or repo, does so
on its own side. An API that does not know the filter ignores it, so `slate
issues list` refuses any row under `--autofixable` whose `is_autofixable` is
missing or does not match. `slate next` and `slate claims hold --next
--autofixable` need no such check, because every API serving `/queue` and
`/claims/next` reads the filter.

Neither status nor someday carries this. Status says where the work is in its
life, and someday means nobody has taken the work on.

## Triage holds what nobody has accepted yet

An issue filed as `triage` sits outside the ready queue until someone accepts
it as `open`. Work noticed in passing is filed there, with `discovered_from`
naming the issue it was found during. It is recorded without jumping ahead of
work someone chose.

## Order is priority, then each initiative's work together, then rank

Priority uses Linear's scale:

| Value | Name |
| --- | --- |
| 1 | urgent |
| 2 | high |
| 3 | medium |
| 4 | low |
| 0 | none |

`0` is the absence of a priority and sorts after every level.

The priority an issue sorts by is its effective priority, the most urgent of:

- its own priority
- its open parent's effective priority
- its active initiative's priority
- the effective priority of any open issue it blocks, at any depth

Nothing in that set lowers it. An own priority raises an issue above its
initiative and never sinks it below, so an issue less urgent than its
initiative belongs outside it.

Raising a blocker is priority inheritance, from scheduling. Without it a blocker
filed at low priority waits at the bottom while the urgent work it gates sits
at the top of the list, hidden because it is blocked.

The queue is a list of slots, and `services/issue_readiness.py` derives each
issue's place in it on every read. A slot is one of:

- an active initiative, with its issues
- an idea, or an active initiative with no unclosed issue
- an issue in no active initiative

Slots sort by priority, then by the slot's rank. Inside an initiative, its
issues sort by their own rank. So an initiative's work runs together, and the
initiative's place decides where all of it falls. An issue whose own priority
is more urgent than its initiative's stands alone, at its own priority.

An issue something waits on sorts just ahead of the earliest issue waiting on
it, at any depth. A blocker is waited on by its dependents, and a child by its
parent. So it comes up before the work it gates, wherever that work sits.

```text
urgent  initiative 12 ── issue 40 (rank 3) ── issue 44 (rank 9)
        issue 51, in no initiative
        initiative 15 ── issue 41 (rank 4) ── issue 43 (rank 8)
high    idea 60
```

`rank` lives on `records`, so ideas, initiatives and issues share one space.
It is a float, the way Linear's `sortOrder` is. A new record goes one past the
last, placed by the `records_rank_at_end` trigger, so a new issue lands at its
initiative's tail. An initiative promoted from an idea takes the idea's rank,
so it keeps the place a reorder gave the idea. A move takes the midpoint of its two new neighbors, so it
writes one row. When a move would leave a gap smaller than `MIN_GAP` in
`services/fractional_rank.py`, every rank is renumbered to whole numbers in its
current order. `number` breaks a rank tie, because it is issued in creation
order and never repeats.

`POST /issues/{id}/rank`, `POST /initiatives/{id}/rank` and
`POST /ideas/{id}/rank` place a record directly before or after a record of
any kind, through one rule in `services/queue_moves.py`:

- A slot moves among the slots of its priority. Named beside an issue inside
  an initiative, it goes beside that whole initiative.
- An issue inside an initiative moves only among that initiative's issues.
- An issue sorting ahead of what waits on it moves with that, so it is placed
  only beside another issue sorting there.
- A record the queue holds no place for neither moves nor is moved beside:
  a closed issue, a someday or finished initiative, a promoted idea. The
  readiness result is the one place that says which records hold a place.

A move that would land anywhere but beside the record named answers 409,
saying why. One across priorities names both, because changing the priority is
what moves a record past another priority's.

- *Rejected:* an initiative `position` beside the issues' rank. Ranks in two
  spaces hold no order between an initiative and an issue or an idea, so
  neither could place an initiative's work among the rest of its priority.

## Who is working an issue is its claim, never its status

An agent takes an issue by holding a claim on it, and `next` lists what someone
holds apart, under its holder. A claim is a lease on a record of any kind, kept
after it ends. [claims.md](claims.md) gives the model: the one refusal, the
warnings, and how a claim expires and ends.

## An issue names the paths it changes, so a hold sees work on the same code

`paths` lists files and directories in the issue's repo, from the repo root.
A directory covers everything under it, the way a git pathspec without
wildcards does. Two paths overlap when they are equal or one is a directory
holding the other. Two issues overlap when they are on the same repo and any
of their paths do.

Holding a claim on an issue warns `claimed_overlapping_paths` for each issue
another holder has under a live claim whose paths meet its own. `next` marks a
queued row the same way, in its `overlaps`, so the collision shows before
anyone takes the work. Nothing refuses: two holders on the same code is often
fine, and a refusal would make both ask twice.

A write normalizes each path, dropping `.` and empty segments, and refuses an
empty path, one climbing out with `..`, and a glob. An issue naming paths
needs a repo, because a path is read in it.

- *Rejected:* globs. Whether two globs can match the same file is a harder
  question than whether one prefix holds another, and the author of an issue
  knows the directory it changes.

## Nothing closes an issue for its age

An issue stays open until someone completes or cancels it, however long it
sits. A stale-close bot closes real work that simply has not come up yet, and
the order already decides what gets done next. So age is never an input to
the order or to the status.

Closing is deliberate in both directions:

- **Completed** is refused while the issue has open children, because a parent
  is finished by its children.
- **Canceled** requires a `status_reason` or a `duplicate_of`, so a closed issue
  always says why.
- **Reopen** returns an issue to `open`, and clears when and by whom it closed,
  the reason and the duplicate link.

Completing or canceling an issue ends its claim and every worklog still open
on it, and the answer names each.

A close records `closed_by` beside `closed_ts`: the actor the client names,
else the token's subject. A token is the person's whichever session or worker
holds it, so only the client can say which one acted. The CLI names whoever
runs it, with the kind of actor as the prefix:

| `closed_by` | Who |
| --- | --- |
| `claude-code/<session>` | a Claude Code session someone attends |
| `claude-code-unattended/<worker>/<session>` | a session a named worker started |
| `claude-code-unattended/<session>` | a session nobody attends, unnamed |
| `worker/<worker>` | a named worker running the CLI itself |
| `user@host` | a person at a terminal |

A scheduler names its worker in `SLATE_ACTOR`. Claude Code sets
`CLAUDE_CODE_SESSION_ATTENDED=0` in a session nobody attends. Claims are
signed the same way. A null `closed_by` means unknown. A move
between completed and canceled keeps the first close's stamps. So `closed_by`
is accepted only on the change that closes an open issue, and any other PATCH
naming one answers 422.

A read narrows on the prefix: `slate issues list --status completed --closed-by
claude-code/` is what attended sessions finished, and `closed_by=` is the same
query parameter on `GET /issues`.

A completed parent never holds open children. A closed issue takes no new open
child, whether filed under it or moved there. Reopening a child returns each
completed ancestor to `open` in the same write, and the answer names each one.

## Labels are a closed vocabulary with exclusive groups

A label must exist before an issue can carry it, so a misspelling is refused
rather than becoming a second label. Labels sharing a `group_slug` exclude each
other, and an issue carries at most one label from each group.

`/labels` and `slate labels` manage the vocabulary.

## An initiative is a bounded outcome

An initiative groups issues toward something with a definition of done, and
lends them its priority. Work with no end is a label instead. An initiative
that can never finish would hold its issues together in the queue forever.
Only an active initiative lends its priority and groups its issues, so
completing or dropping one leaves each issue still open inside it standing
alone.

## Not built

- **Due dates.** An issue is ordered by priority and rank, never by a date it
  missed. `deferred_until_date` is the one date, and it only holds an issue
  back.
- **Sprints and estimates.** Both measure a team's throughput against a
  calendar. The queue here is taken one issue at a time in order.
- **Custom workflow states.** The statuses are `triage`, `open`, `completed`
  and `canceled`. Anything finer is a label.
