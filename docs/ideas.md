# Ideas

An idea is work that nobody has taken on yet. `slate ideas` is the CLI, and the
API serves it under `/api/v1/ideas`.

## An idea is never work, so it has no status

An idea records what the work would be, how urgent it is, and what it rests
on. Promoting it creates the initiative that is the work.

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
`GET /ideas?ref=capture/`. An idea's read also carries them as `evidence`,
the name it served before refs, while that release still serves.

## Promoting an idea creates its initiative, which names the idea

`POST /ideas/{id}/promote` creates an initiative with `idea_id` naming the
idea. Each field that the request omits takes the idea's own: its title as the
name, its description, its priority. The initiative carries a copy of each of
the idea's refs, named as `refs_carried`. The answer is the idea, which then carries
the initiative with its issue counts. Its `side_effects` name the initiative
created, as `initiative_created`. A claim on the idea ends as `promoted`, named
as `claim_ended`, because the idea's work is the initiative's from then on.
Every worklog still open on the idea ends as `promoted` too, each named as
`worklog_ended`.

An initiative read names its idea as `idea`. An idea becomes at most one
initiative, so a second promotion answers 409 naming the first.

Each link holds its parent's id, so the chain from idea to initiative to issue
reads back from any point. That is data lineage, W3C PROV's "derived from".

A claim on a promoted idea goes through with an `idea_promoted` warning naming
the initiative, because the work lives there now.

## Deleting an idea takes its refs, and a promoted idea is refused

An idea has no status to close it, so an idea recorded by mistake leaves the
queue only by its delete. `DELETE /ideas/{id}` deletes the idea's record, as
`records.md` describes for every kind. Its refs, its pin, and every claim
and worklog on it go with it. The answer names the claims and worklogs as
`claims_deleted` and `worklogs_deleted`.

A promoted idea answers 409 naming its initiative. The initiative's `idea_id`
is where its work came from, and would quietly set to null if the idea went.
