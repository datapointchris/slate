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
- its initiative, while open, waits on no open initiative or issue
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
| `shape` | an active initiative with no checked step and no unclosed issue |
| `design` | an active initiative whose furthest checked step is `shape` |
| `preflight` | an active initiative whose furthest checked step is `design` |
| `decide` | a ready `decision` issue |
| `build` | any other ready issue, or a held initiative whose issues are being built |
| `retro` | an active initiative past `design` with no unclosed issue |

An initiative's step is the one after its stage, the furthest step its
worklogs checked, which [worklogs.md](worklogs.md) defines. These are the
worklog's own step names. So `slate next --step shape` is the work still to
think through, and `--step preflight` the work to check before agents build it
unattended.

`step` keeps one step's rows, so `slate next --step build` is the work an agent
takes. A step never holds work back. An initiative at `design` or `preflight`
is a row beside its open issues, which still list at `build`. An initiative at
`build` is not a row of its own, because its issues are. That holds for one
past `design` with unclosed issues, and for one with unclosed issues and no
checked step, which was built before steps existed. It is listed only when
held, in `claimed`, at step `build`, so whoever holds it is seen beside the
issues it gathers. `repo`, `type`, `label` and `autofixable` read only issues, so
each leaves the ideas and initiatives out. `initiative` narrows to one
initiative's rows.

Each row says its `kind`, `number`, `step` and `priority`, and carries the
kind's own read as `record`. `priority` is the one the row sorts at: an
issue's effective priority, else the record's own.

A live claim says someone is working the record, so a held record is listed
apart in `claimed`, by holder, whatever its readiness. A live claim on an
initiative lists the ready issues to build in its plan there too, and
[claims.md](claims.md) says what that holds and what it leaves alone. The rest
are `queue`, first row next. `limit` caps `queue` and never `claimed`.

`POST /claims/next` holds the head of the `build` rows under the same filters,
and the initiative's holder can also take its own plan's issues. `queued_issues`
selects and orders the issues for both, and `held_plans` says which of them a
held plan takes, so the preview and the hold cannot disagree on either.

**`for_record` reads one plan whole.** A coordinator taking a plan to done needs its issues and
everything they wait on, in any initiative or repo. `initiative` cannot answer that, because it
drops a dependency held elsewhere. So `slate next --for <record>` reads the transitive closure from
the record's open issues, as `bazel query 'deps(//target)'` reads a build graph. An issue waits on
what it depends on and on its children, and the walk stops at a closed issue. An issue in an
initiative that waits on an open record also waits on that issue, or on each open issue of that
initiative, because an initiative is finished by its issues. The rows include work
that is not ready yet, because that is the plan, and each names in `unblocks` which rows wait on it
directly. The CLI's default cap does not apply, since a cut plan reads as a smaller one.

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

### An initiative waits on an initiative or an issue, and holds back every issue in it

A plan that starts after another plan says so with an edge from the initiative:
`slate dependencies add <initiative> --depends-on <record>`. While the waiting
initiative and the record it names are both open, every unclosed issue in the
initiative reads `is_blocked`. An initiative is open until it is finished, so a
someday initiative counts as open. An issue is open until it closes. A finished
initiative's wait holds nothing, as a finished initiative lends no priority.

- **`POST /records/{ref}/dependencies` writes either kind's edge.** The server
  reads which kind `ref` names, by number, UUID or an initiative's name, so no
  client guesses it. `/issues/{id}/dependencies` still writes an issue's edge.
- **The issue's read says what blocks it.** `blocked_by` lists each open record
  holding it back now, whatever the cause, and is empty exactly when `is_blocked`
  is false. An entry its initiative's wait holds names that initiative in
  `through_initiative`. `initiative_waits_on` lists every record an unclosed
  issue waits on through its initiative, open or closed, as `depends_on` lists
  every dependency. The initiative's read lists `depends_on`, and each record's
  read lists the `waiting_initiatives`.
- **A loop through any mix of edges is refused on write.** An initiative waits on
  its unclosed issues, since it is finished by them, and each of them waits on
  what the initiative waits on. So an initiative waiting on one of its own issues
  leaves that issue waiting on itself. Moving an issue into an initiative or
  reopening one is checked the same way, as is a dependency or a parent that
  would close a loop through an initiative. The refusal prints the loop. Each
  check holds one advisory lock until its write commits, so two writers each
  adding half of a loop cannot both pass.
- **The wait lends no priority.** An issue raises what it depends on, but an
  initiative's wait leaves the order of the work it waits on alone. The two
  plans are placed with `slate initiatives reorder`.
- **It brings no someday work into the queue.** A wait on a someday initiative
  blocks the waiting one's issues and leaves the someday issues shelved.

`initiative_dependencies` keys the waiting end on `initiatives.id`, as an issue's
edges key on `issues.id`, and the waited-on end on `records.number`, because that
end may be either kind. The API refuses an idea there.

- *Rejected:* one table of edges over `records` for every kind, replacing
  `issue_dependencies`. Every existing edge would move, the API one release
  behind reads the old table during a rollout, and it would admit an issue or an
  idea waiting on an initiative, which nothing needs.

## A someday initiative holds planned work until active work needs it

An initiative is `someday` when its work is planned and nobody has taken it
on. A thought not yet worked out is an [idea](ideas.md) instead. A someday
initiative's issues are captured in full, but the ready queue leaves them out,
so no agent takes work nobody chose. Reopening the initiative makes them ready. Moving one issue into
an active initiative takes that one alone.

An issue outside someday that depends on a someday issue, directly or through a
chain, has chosen it. That someday issue enters the queue at the dependent's
priority and stays in its initiative, because it belongs with that idea while
something urgent needs it first. Closing the dependent shelves it again.

`someday` is neither active nor terminal. It lends no priority, and like a
closed initiative it gives up its name, so an active one may take it.

## Work set aside keeps its repo and takes `low`, and a defer date is a day someone named

An issue set aside for now keeps its repo and takes `--priority low`. It leaves
the head of the queue and comes back up when someone works in that repo. Where
it is all that is left of its initiative, the initiative goes to `low` too,
because an issue sorts at its initiative's priority when that is higher. A
`--deferred-until` date nobody chose is a false fact in the queue, so it is set
only to a day a person named.

An initiative placed behind another stays active. `slate initiatives reorder
<initiative> --after <other>` puts it next, and `slate dependencies add
<initiative> --depends-on <other>` blocks its issues until the other closes.
Shelving it to `someday` instead takes it out of the queue entirely.

## An issue reaches an unattended fix only where it and every record above it say `autofix: true`

An unattended fix is a commit nobody reads before it lands. Two kinds of work
must stay out of one. A step of a larger design would land alone, out of the
design it belongs to. That design is an initiative, or a parent issue split
into children. A standalone issue may need a person, or someone may simply
want to do it themselves.

Issues and initiatives each carry `autofix`, false by default, so a record
filed without a word about it stays with a session. Opening one is a choice
someone makes. An issue is autofixable when it says true and none of its
initiative and its unclosed parent says false, and the parent is read the same
way up the tree. That is derived on every read with readiness and returned as
`is_autofixable`. So an issue filed later into an initiative that says false,
or under such a parent, is kept out whatever it says itself.

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
work someone chose. `slate issues create` fills `discovered_from` with the
issue the session's worklog is on, so the link costs a session nothing. A
retrospective then reads an initiative's trailing fixes as the issues
discovered from its own.

## Order is a person's pin, then priority, then each initiative's work together, then rank

A pin puts a record ahead of every priority. [Its own section](#a-pin-holds-a-record-above-every-priority-until-it-is-lifted-or-the-record-closes)
says what it reaches. Below the pins, priority decides.

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

## A pin holds a record above every priority until it is lifted or the record closes

`slate issues pin`, `slate initiatives pin` and `slate ideas pin` are the one way
work crosses a priority. A pin is a term ahead of the priority in the sort key, so
pinned records still sort among themselves by priority, then rank. `slate next`
says `pinned` on each row a pin put there, and `pinned` in its JSON says the same.
`pinned_ts` is set only on the record a person pinned. `slate initiatives list`
sorts by the same term, so a pinned initiative lists first.

```text
pinned  issue 51
        initiative 12 ── issue 40 ── issue 44
urgent  initiative 15 ── issue 41
high    idea 60
```

- **An initiative's pin brings all its issues.** That includes one whose own
  priority would otherwise stand it alone. A pinned issue inside an unpinned
  initiative stands alone at its pin.
- **What a pinned issue waits on sorts just ahead of it**, taking the pin with it,
  by the same rule that raises a blocker. Its effective priority follows.
- **Only a person pins.** No scope lists the routes, so a scoped client answers
  403. An automated order can then never pin its own favorite. A token belongs to
  a person whichever session holds it, so `pinned_session` names the agent session
  that sent it, where one did, beside `pinned_by`.
- **A pin ends with the record.** Closing an issue, or finishing or shelving an
  initiative, clears it and answers `pin_cleared`. A promoted idea's work is its
  initiative's, so the pin moves there and answers `pin_carried`. Reopening
  restores no pin.
- **A record the queue would not show is refused with 409:** a closed issue, an
  issue in a `someday` initiative, an initiative that is not active, and a
  promoted idea.
- **A move across the pin answers 409,** as one across priorities does. Pinning or
  unpinning is what moves a record past that line.

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
else the token's username, else its subject. A token is the person's whichever session or worker
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

**A summary says what is true when it is done.** It is one line of at most 240
characters, in words the name does not use, and quotes, dates and history stay in
the description. A create and a promotion require it, and an edit can change it
but never clear it. It is a field rather than the description's first paragraph,
so a program reads it and the schema holds its shape, where a parse would have to
guess which words were meant as the summary.

**An initiative reads which steps of its life it went through.** Every read of
one answers `steps`, each step a worklog of it or of its idea checked, in
position order, with that worklog's name, when it ended and the first session
in it. `stage` is the
furthest, or `idea` before any. `services/record_steps.py` derives both on every
read from the worklogs, so no stage is set by hand and none goes stale.
[worklogs.md](worklogs.md) says when a step is checked. `slate show` prints a
`steps:` line, and `slate initiatives list` prints the same marks under each
initiative with a checked step:

```text
  2804  ▰▱▱▱▱▱▱▱▱▱  1/10  Initiatives record the steps they went through  slate
        ✓shape 11-09  ✓design 11-10  · preflight  · build  · retro
```

## An initiative's progress counts each of its issues in one state

`GET /initiatives/progress` answers how far each initiative has come, and
`slate initiatives progress` prints it. It is the view Jira's epic progress bar
and Linear's project view give. Each issue counts in exactly one state, so the
counts add up to every issue in the initiative, closed ones included. An issue
takes the first state that holds:

| State | When |
| --- | --- |
| `completed`, `canceled` | its closing status |
| `running` | a live claim holds it |
| `waiting` | a live claim holds it, and its holder parked it with `slate claims wait` |
| `triage` | it was filed and nobody has accepted it |
| `blocked` | it depends on an unclosed issue, or its initiative waits on an open record |
| `ready` | `next` would hand it out |
| `deferred` | its not-before day has not come |
| `shelved` | its initiative is someday, and nothing outside someday depends on it |
| `open_children` | only its open children hold it back |

A claim outranks every unclosed state. A held issue is being worked, whatever
else is true of it. Triage outranks blocked, because nobody has accepted the
work its blockers hold back. No status says an issue is in progress. Its claim
says that.

One read answers any number of initiatives. Each `initiative` names one, in
the order named. With none named, the answer holds every active initiative in
the order to work them. Readiness is measured over the whole issue graph, so
one pass serves them all. The route's path is the name `progress`, so no
initiative may take that name.

For each initiative, the answer lists the issues running, waiting and blocked,
in queue order. Each carries what it depends on, so a held issue still shows
what holds it back. `next` is the
head of `GET /queue` for the initiative. It is read from the same readiness
pass, so the two never disagree. `closed` lists the closes inside the `since`
and `until` window, latest first. `totals` is what `GET /totals` answers for
the initiative over its whole life, whatever the window.

**`burnup` is the initiative's issues over its whole life**, the burn-up chart Jira
draws for a release. Each step holds `scope`, the issues in the initiative and not
canceled, and `completed`, those of them completed, from its `ts` until the next
step. A step falls at each moment either changed, and a last one at the read, so
the last step is the counts above.

An issue joins an initiative when a write sets its initiative, and leaves when one
moves it elsewhere. The audit trail holds every such write, so membership is read
back from it rather than stored twice. One read of the trail's `issues` rows, and
one of the issues that have since left, serve every initiative the read answers,
however many it answers. An issue with no write that joined it was
in the initiative from its creation, as an issue imported without its history was.

Closes are read from each issue as it stands. A canceled issue leaves the scope at
its close. A completed one counts from its close, or from when it joined where that
came later. A close at no recorded time counts from when the issue joined, so the
last step still agrees with the counts. A reopened issue counts as it stands now,
and a deleted one is in no step.

- *Rejected:* an issue's creation as its join. An issue moved in later would read
  as scope the initiative held all along, which is the growth the chart is for.

The window means what it means on every bounded read. A bound left out leaves
that end open, so the API given no `since` lists every close. "Recently" is the
CLI's choice. Given neither `--since` nor `--until`, it reads the closes since
the default its help shows. `--since=` lists every close. The answer echoes the
window as `closed_since` and `closed_until`.

- *Rejected:* a recent default in the API. `since` would mean one thing on this
  route and another on every other.
- *Rejected:* a CLI composing the initiative, issue, queue and totals reads.
  That is four calls per initiative, and the states derive from readiness,
  which only the API derives.
- *Rejected:* `GET /initiatives/{id}/progress`. A name may hold a slash, and
  the path is decoded before routing, so the read of an initiative named
  `Ship/progress` would answer the progress of `Ship`. It also took one call,
  and one readiness pass, per initiative.

The bar spans the done and open issues and leaves out the canceled ones, as
the bar in `slate initiatives list` does. Done fills it in green. Its empty
cells are yellow for the work under way and red for the blocked, drawn by the
same `TallyBar` as `slate tui`'s bar. So without color it still reads as done
over done and open, with the blocked marked. `--json` prints an array,
one entry per initiative, carrying every figure the text shows.

## The board read answers every initiative's counts, closes and issues, cheap enough to poll

`GET /initiatives/board` is what a live board polls. For each initiative at the
`status` asked for, active by default and in the order `GET /initiatives` lists
them, it answers the counts by state above, the repos, the pin, the live claim,
the closes, the checked steps and stage, and a slim row per issue: the unclosed in queue order, then the
closed, latest first. `totals` and `tiers` roll the same counts and closes up over
every initiative and per priority. Every priority is in `tiers`, whether or not
an initiative is at it.

**Each initiative's `by_repo` cuts its issues by the repo each names, and `repos`
rolls those cuts up per repo.** Both come most open first, with no repo last.
An initiative with no issues has one empty cut on no repo, so a board grouping by
repo draws every initiative somewhere. A repo's `initiative_count` is the
initiatives with a cut there. The cuts are counted by the same `counts_of` as the
tiers, so a client grouping by repo draws numbers it is handed rather than
counting issues itself.

Its queries do not grow with the initiatives. One readiness pass, one read of the
live claims, one read of the issues and one of the checked steps serve them all,
and everything else folds those rows in memory. It builds no `issue_views` row, because that loads labels,
dependencies and comments a board does not draw. A poll pays this many times a
minute, for as long as a board stays open, so a test pins the query count.

**A close is a completed issue.** `closes` counts back from the moment of the read
over `last_24h`, `last_7d` and `last_30d`. `by_day` counts one entry per day of the
answer's `days`, the last 14 calendar days in the request's `timezone`. A canceled
issue never counts as a close, because the question is how much work finished. A
completed issue whose close has no recorded time counts in `states` and in no window.

**Long text is a second read.** `GET /initiatives/board/text` answers each
initiative's summary and whole description, and an excerpt of each of its issues.
Descriptions are most of what a list of initiatives weighs, so the poll never
carries them and a board reads text less often. An issue's excerpt is the first
paragraph holding prose, with headings, code, table rows and rules skipped and
inline markdown stripped, cut at a word near 300 characters. It is derived on
read, and walking a description is too cheap to be worth storing. An issue's whole
description stays on `GET /issues/{id}`.

The paths `board` and `board/text` are names no initiative may take, as `progress` is.

**`slate initiatives board` prints the board read, with each initiative's bar.** Its
`--json` adds `bar` to each initiative: `done`, `under_way`, `blocked` and `rest`, split
by the one rule every bar slate draws keeps. Under way is running and waiting, and the
rest is what open leaves after both. So a dashboard drawing an initiative beside
`slate tui` draws the numbers it is handed, and the split stays slate's. Its text rows
draw `progress`'s bar.

- *Rejected:* a dashboard reading `slate tui --json` for the counts. That is the tab's
  own state, shaped by what the screen draws, and it also reads the long text and the
  cursor's row.

- *Rejected:* polling `GET /initiatives/progress`. It answers whole issues with
  everything they join, and its `totals` walk each initiative's issue tree for
  worklogs and tokens, which a board draws for one row at most.
- *Rejected:* a `limit`. `totals` and `tiers` need every initiative whatever the
  board shows, so a board cuts its scope in the client.

## The repo board read answers each repo's pace and how long its ready work has waited

`GET /repos/board` is what `slate tui`'s repos tab polls. A repo is a name on an
issue rather than a record, so the read groups every issue by that name. The issues
on no repo make a row of their own, with a null `repo`. Rows come by name.

Each row carries the initiative board's counts by state and its closes. Its
`closes_by_week` holds one count per week of the answer's `weeks`. Those are the
Mondays of the last `CLOSES_BY_WEEK` weeks in the request's `timezone`, the last the
week of the read. `ready` lists the repo's ready issues, the longest waiting first.
`totals` rolls the counts and closes up over every issue.

**A ready issue has waited since it was filed.** Readiness is derived on every read
and never stored, so no moment an issue became ready is recorded to count from.

Its queries do not grow with the repos, as the initiative board's do not. One
readiness pass, one read of the live claims and one read of every issue's slim
columns serve every row.

- *Rejected:* a route per repo. Nothing but an issue names a repo, so no record
  stands behind the path.
- *Rejected:* the wait's spans in the API. Each ready issue is in the answer, so
  a reader picks spans to fit its screen and names the oldest by title.

## Not built

- **Due dates.** An issue is ordered by priority and rank, never by a date it
  missed. `deferred_until_date` is the one date, and it only holds an issue
  back.
- **Sprints and estimates.** Both measure a team's throughput against a
  calendar. The queue here is taken one issue at a time in order.
- **Custom workflow states.** The statuses are `triage`, `open`, `completed`
  and `canceled`. Anything finer is a label.
