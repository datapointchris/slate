# Ideas

An idea is work nobody has taken on yet. `slate ideas` is the CLI, and the API
serves it under `/api/v1/ideas`.

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

## Evidence is a key or a URL that slate never follows

`POST /ideas/{id}/evidence` links refs, each one row in `idea_evidence`. A ref
is a key the system holding the evidence gave it, such as an inbox row's, or
an http or https URL. The read derives `kind` from the ref: `url` for an http
or https URL with a host, `key` for anything else.

slate stores a ref as given. It never resolves a key or fetches a URL, so a
source can move or vanish without slate knowing, and no source needs slate to
understand it.

A ref the idea already links is left with its note and linker, and the answer
warns `evidence_already_linked`. Each idea links a ref once.

## Promoting an idea creates its initiative, which names the idea

`POST /ideas/{id}/promote` creates an initiative with `idea_id` naming the
idea. Each field the request leaves out takes the idea's own: its title as the
name, its description, its priority. The answer is the idea, which then carries
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
