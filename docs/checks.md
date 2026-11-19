# Checks

The queue orders by priority and rank alone, so an issue filed against code that has since moved
keeps its place until someone reads it again. A check is that reading: one row saying what an issue
or an initiative was found to be, when, by whom, and on what evidence.

## A check is one row, added and never edited

`record_checks` holds each check, hanging off `records` by an `ON DELETE CASCADE` key, so
`slate show` lists checks among the record's changes and deleting the record takes them.

| Column | Holds |
| --- | --- |
| `checked_ts`, `checked_by` | when, and the caller: a service's client id, or a person's actor |
| `verdict` | one row of the lookup `check_verdicts` |
| `evidence` | what the check found, in prose |
| `cites` | each source the evidence rests on, as `{kind, ref}` |
| `run`, `session` | the scheduled run and agent session that made it, where one did |

A check is never edited, so a record's checks read as the history of whether it still held.
`GET /records/{number}/checks` and `slate checks list` read them latest first. Only issues and
initiatives are checked: an idea holds no work yet, and a pursuit is a direction.

The verdicts are a lookup table, so a reader takes the list from `GET /vocabulary`'s
`check_verdicts` rather than keeping a copy:

| Verdict | What the check found |
| --- | --- |
| `holds` | it still describes work to do, as written |
| `corrected` | it does, once what was wrong in it is rewritten |
| `done` | the work is done |
| `gone` | the work no longer applies |
| `duplicate` | another record carries the same work |
| `contradicts` | it asks for what a standard, a decision or another record rules out |
| `unclear` | nothing read settles it, so it comes due again at its interval |

slate records the verdict and acts on none of them. Whoever made the check closes, cancels or
rewrites the record through the routes every other client uses.

## The latest check rides on every read

Every read of an issue or an initiative carries `last_check {verdict, checked_ts, checked_by}`:
`slate show`, the lists, and each `/queue` row's `record`. It is derived on the read, one query per
list, so it cannot drift from the checks it summarizes.

## A check request marks a record to be checked next

Someone who doubts a record still holds marks it with `PUT /records/{number}/check-request` and a
reason, or `slate checks request`. The mark is three columns on `records` in the pin's shape:
`check_requested_ts`, `check_requested_by` and `check_requested_reason`. Every read carries it as
`check_request`.

- **The next check clears it,** and the check's answer names that as `check_request_cleared`.
- **A second request keeps the first,** and warns `already_check_requested`, so a record keeps its
  place among the marked.
- **`slate checks withdraw` clears it** without a check, through `DELETE` on the same path, as
  `unpin` lifts a pin.

## slate decides which records are due

`GET /checks/due` answers the open issues and initiatives due a check, first due first, and
`slate checks due` prints it. slate decides, so two readers, or one that restarted, read the same
answer and keep no state of their own. Each record is due at the first rung it meets, and each row
says which as `due_because`:

1. **`incoming`**: an issue nobody has accepted yet, most urgent first, then oldest. It stays on
   this rung until a check reads it, and returns once that check is older than
   `SLATE_CHECK_HEAD_DAYS`.
2. **`requested`**: a record under a check request, the oldest request first
3. **`never_checked`**: a record with no check, in queue order
4. **`queue_head`**: one of the queue's first `SLATE_CHECK_HEAD_ROWS` rows whose last check is
   older than `SLATE_CHECK_HEAD_DAYS`
5. **`stale`**: any other record whose last check is older than `SLATE_CHECK_DAYS`, the oldest
   check first

README's settings table gives each default.

The head is read more often because it is the work about to be taken. The head is the queue's
first rows as `slate next` lists them, ideas included, so an idea takes a row and is never due.
The priority on a row is an issue's effective priority, as the queue's is.

These are never due:

- **A record under a live claim,** because a worker holds it. That includes each issue to build in
  the plan of a claimed initiative, as the queue lists it under that holder. A decision in that plan
  waits on a person, so the plan does not hold it and it still comes due.
- **An idea,** which holds no work yet.
- **A closed issue, and a finished or dropped initiative.** A someday initiative is open, so it
  comes due.

Each row carries `likely_duplicates`: the open records whose titles read like it, by the measure
`slate issues create` warns with, most alike first. A reader starts from the candidates rather than
searching for them. `limit` caps the rows before their duplicates are scored, so one read of the
open titles serves the whole batch.

## Who reaches which route

| Route | Scope |
| --- | --- |
| `GET /records/{number}/checks`, `GET /checks/due` | `slate.records.read` |
| `PUT` and `DELETE /records/{number}/check-request` | `slate.checks.request` |
| `POST /records/{number}/checks` | a person only |

A mark only moves a record up the due order and writes nothing a person reads as its meaning, so
a job ordering the work may hold the scope that sets it.

## Rejected

- **Closing by age,** as a stale bot does. Age orders the checks, and a record still closes only
  when someone completes or cancels it.
- **The latest check stored on the record.** A second copy of what `record_checks` holds would
  drift from it, and one read per list derives it.
- **A reader keeping its own schedule.** The due order reads the queue's head and the claims, which
  are slate's, so slate computes it.
