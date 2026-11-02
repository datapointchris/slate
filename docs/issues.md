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

`GET /issues/ready` is the queue, in the order to take it. `slate issues next`
prints it.

## A decision waits on a person, so the ready queue leaves it out

The `decision` type is the one type that changes what happens to an issue.
It needs a person to choose, and an agent that held one could only stall.
The ready queue drops it.

The other types are `bug`, `feature`, `task` and `chore`. They differ only in
what they say about the work, and `task` is the default.

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
whole queue with `slate issues next`. `slate issues next --autofixable` is the
queue for an unattended fix, and is `GET /issues/ready?autofixable=true`. The
filter is named for `is_autofixable`, the derived fact it reads, the way
`blocked` reads `is_blocked`. `autofix` stays the stored setting.

The flag is a veto the reader asks to honor. A hold by number takes an issue
whatever its flag says, and a fixer narrowing further, by type or repo, does so
on its own side. An API that does not know the filter ignores it, so `slate
issues next` refuses any row under `--autofixable` whose `is_autofixable` is
missing or does not match. `slate claims hold --next --autofixable` needs no
such check, because every API serving `/claims/next` reads the filter.

Neither status nor someday carries this. Status says where the work is in its
life, and someday means nobody has taken the work on.

## Triage holds what nobody has accepted yet

An issue filed as `triage` sits outside the ready queue until someone accepts
it as `open`. Work noticed in passing is filed there, with `discovered_from`
naming the issue it was found during. It is recorded without jumping ahead of
work someone chose.

## Order is effective priority, then rank, then number

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

`rank` breaks ties inside a priority. It is a float, the way Linear's
`sortOrder` is. A new issue goes one past the last. A move takes the midpoint
of its two new neighbors, so it writes one row. When a move would leave a gap
smaller than `MIN_GAP` in `services/issue_rank.py`, every rank is renumbered to
whole numbers in its current order. `number` breaks a rank tie, because it is
issued in creation order and never repeats.

A move names the issue to sit before or after, and that issue must sort at the
same effective priority. Ranks are one global sequence, so a move beside an
issue of another priority would land wherever the other priorities' ranks fall.
Such a move is refused with a 409 naming both priorities. Changing the priority
is what moves an issue past another priority's.

## Who is working an issue is its claim, never its status

An agent takes an issue by holding a claim on it, and `next` leaves out an
issue someone holds. A claim is a lease on a record of any kind, kept after it
ends. [claims.md](claims.md) gives the model: the one refusal, the warnings, and
how a claim expires and ends.

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

Completing or canceling an issue ends its claim, and the answer names whose
claim it ended.

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
`CLAUDE_CODE_SESSION_ATTENDED=0` in a session nobody attends. Comments and
claims are signed the same way. A null `closed_by` means unknown. A move
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

`/issues/labels` and `slate issues labels` manage the vocabulary.

## An initiative is a bounded outcome

An initiative groups issues toward something with a definition of done, and
lends them its priority. Work with no end is a label instead. An initiative
that can never finish would rank its issues forever by a position nobody
revisits. Only an active initiative lends its priority, so completing or
dropping one stops it ranking the issues still open inside it.

## Not built

- **Due dates.** An issue is ordered by priority and rank, never by a date it
  missed. `deferred_until_date` is the one date, and it only holds an issue
  back.
- **Sprints and estimates.** Both measure a team's throughput against a
  calendar. The queue here is taken one issue at a time in order.
- **Custom workflow states.** The statuses are `triage`, `open`, `completed`
  and `canceled`. Anything finer is a label.
