# Ideas

An idea is work that nobody has taken on yet. `slate ideas` is the CLI, and the
API serves it under `/api/v1/ideas`.

## An idea is never work, so it has no status

An idea records what the work would be, how urgent it is, and what it rests
on. Promoting it turns it into the initiative or issue that is the work, under
the same number.

An idea carries its own priority on the scale every record shares. A
high-priority idea is one worth deciding on soon. Nothing about being an idea
caps how urgent it can be. `slate next` lists an unpromoted idea at step
`discuss`, at its priority, and `slate ideas reorder` places it.

This is product discovery, as Jira Product Discovery runs it: an idea collects
its insights, and once committed it links the delivery work, whose progress
rolls back up to the idea.

- *Rejected:* a `someday` initiative for every idea. A someday initiative is
  work already designed and held back. An idea not yet designed has no
  issues to hold, and filing it as an initiative claims a plan that does not
  exist.

## What an idea rests on is its refs, keys or URLs that slate never follows

An idea links what it rests on as refs, as initiatives and issues do.
`records.md` describes them: one `record_refs` row per record and ref, linked
on create or by `POST /ideas/{id}/refs`, and found again by prefix with
`GET /ideas?ref=capture/`.

## Promoting an idea changes its kind and keeps its number and every row on it

`POST /ideas/{id}/promote` changes the record's kind in place. `kind` is
`initiative`, the default, or `issue`. The record keeps its number, its `id`
and its `created_ts`. Its refs, tags, comments, pin, rank, flow, claims and
worklogs all hang off the number, so each stays where it was and nothing is
copied. `records.promoted_ts` and `promoted_by` say when the kind changed and
who changed it.

This is a subtype change under class table inheritance. Each kind's table keys
its row on `records(number, kind)` with no `ON UPDATE`. So one transaction
locks the `records` row, deletes the `ideas` row, sets `records.kind`, and
inserts the new kind's row under the same `id`. A number that another kind
holds answers 409.

Each field that the request omits takes the idea's own: its title as the
initiative's name or the issue's title, its description, its priority, its
tags. An initiative needs a `summary`, which an idea lacks. An issue goes
through every check that `POST /issues` runs, and starts `open`. A field of the
other kind answers 422 naming it.

An issue takes its initiative's pursuits and holds none of its own. So a
promotion to an issue drops each of the idea's pursuits, with a
`pursuits_dropped` warning naming each.

The answer is the initiative's or the issue's write. A claim on the idea ends
as `promoted`, named as `claim_ended`, because the work under that number is
the new kind's from then on. Every worklog still open on it ends as `promoted`
too, each named as `worklog_ended`. Promoting into `someday` clears the pin, as
shelving an initiative does, and answers `pin_cleared`.

The initiative's answer also carries `idea`, the idea as it was, with
`idea.initiative` naming the record itself. CLI releases through `cli/v0.66.0`
read a promotion's answer from that member. A promoted idea's UUID names the
new kind's row, so a read or a promotion by that UUID answers as one by its
number does.

`GET /records/{n}/changes` reads every kind row that held the record's `id`, so
the idea's writes stay in the number's history after it is an initiative or an
issue.

- *Rejected:* creating a new initiative that names the idea, and copying the
  refs, tags, pursuits, pin, rank and flow across. Each copy was one more path
  to keep level with the schema, and the work changed numbers at the moment it
  started. `merged-and-moved-ids-prior-art.md` gives what other trackers do.

### An initiative that names an idea in `idea_id` holds its work under another number

An initiative whose `idea_id` is set came from that idea under a number of its
own, and reads it back as `idea`. Promoting such an idea again answers 409
naming that initiative. A claim on it goes through with an `idea_promoted`
warning naming the initiative, because the work lives there.

## Folding an idea moves every row on it to the work that covers it, and its number leads there

An idea that existing work already covers is folded into that work rather than
promoted. `POST /ideas/{id}/fold` takes `into`, the number of an idea,
initiative or issue, and `holder`, whose claim on the idea moves with it. The
holder defaults to the caller. `slate ideas fold <idea> --into <record>` sends
it, and no service scope reaches it.

One transaction does the whole fold:

1. **It locks both `records` rows `FOR UPDATE`, lower number first.** Two folds
   over the same pair then queue rather than deadlock.
2. **It refuses with 409** a record that is not an idea, a pursuit as `into`,
   the idea as its own `into`, and another holder's live claim on either
   record. It refuses an idea that an initiative's `idea_id` names, as the
   delete does, because that initiative names it as where its work came from.
   A closed `into` is taken, because the idea's work may already have shipped
   there.
3. **It settles the claims.** A claim whose lease ran out ends as `expired`.
   The holder's claim on the idea moves to the record. Where the holder holds
   the record too, the idea's claim ends as `released` instead.
4. **It moves the rows.** Worklogs are numbered on after the record's last, and
   each old name is kept in `worklog_redirects`. Claims with their waits,
   comments and checks move as they are. A document whose name the record
   already holds moves as `<name>-from-<idea>`, and its line comments follow
   through the `ON UPDATE CASCADE` key on `comments`. Each document's old
   number and name are kept in `document_redirects`, mapped to its `id`, so a
   later fold that moves it again leaves no chain. Refs, tags and pursuits
   move where the record lacks them. An issue holds no pursuits, so a fold into
   one drops the idea's.
5. **It retargets every redirect that led to the idea**, so a retired number
   always leads one step.
6. **It deletes the idea's `ideas` and `records` rows** and keeps the number in
   `record_redirects`, with the idea's `id`, title, priority, flow and
   `created_ts`.

The answer is the record's own write: `idea`, `initiative` or `issue`, as its
kind's routes answer. Its side effects name each move:

| Code | When |
| --- | --- |
| `expired_claim_ended` | a claim on either record had run out; it ends as `expired` |
| `claim_moved` | the holder's claim on the idea moved to the record |
| `claim_ended` | the holder held both, so its claim on the idea ends as `released` |
| `worklog_moved` | each worklog, with its old and new names |
| `claims_moved`, `comments_moved`, `checks_moved` | the count of each that moved, and the waits on the claims |
| `document_moved` | each document, with its new address and why it was renamed |
| `refs_moved`, `tags_moved`, `pursuits_moved` | the count of each the record did not already carry |
| `redirect_retargeted` | a number that led to the idea now leads to the record |
| `record_redirected` | the idea's number now leads to the record |

It warns `pursuits_dropped` for each pursuit a fold into an issue drops. It warns
`pin_dropped` where the idea was pinned, because the record keeps its own place
in the queue.

An edit, pin or promotion of the idea takes the idea's `records` row
`FOR NO KEY UPDATE` before it reads the idea, so it waits while a fold holds
that row. Once the fold commits, the row is gone, and the write reads the
number again and answers the same 409 a write through the redirect gets. A
comment or another insert that names the idea's number fails its foreign key
on the deleted row, and the API answers that with the 409 too. `records.md`
says how a retired number reads. It also says what a delete does to the
numbers folded into the deleted record.

- *Rejected:* keeping the idea as a stub that points at the record, as Stack
  Overflow keeps a merged question. Every list and the queue would then have to
  skip the stub. A redirect row is read only where a number misses.
  `merged-and-moved-ids-prior-art.md` gives what other trackers do.

## Deleting an idea takes its refs, and an idea that names its initiative is refused

An idea has no status to close it, so an idea recorded by mistake leaves the
queue only by its delete. `DELETE /ideas/{id}` deletes the idea's record, as
`records.md` describes for every kind. Its refs, its pin, and every claim
and worklog on it go with it. The answer names the claims and worklogs as
`claims_deleted` and `worklogs_deleted`.

An idea that an initiative names in `idea_id` answers 409 naming that
initiative. The initiative's `idea_id` is where its work came from, and would
quietly set to null if the idea went.
