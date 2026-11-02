# Totals

`slate totals` sums the work over one scope: how many worklogs, sessions,
commits, pull requests, landings and messages it holds, and its tokens by kind.
The API serves it at `GET /api/v1/totals`, and `slate.records.read` reaches it.

## Every total is derived on read, and none is stored

Tokens come from `message_tokens`, links from `worklog_links`, and sessions and
worklogs from `worklog_spans` and `worklogs`. Each read sums those rows over
its scope. So a report that replaces a session's tokens, or a link added to a
worklog that ended, shows in the next read with nothing to recompute.

This is a rollup over a star schema, the shape a data warehouse reports from.
`message_tokens` and `worklog_links` are the fact tables. Record, repo and time
are the dimensions, and each scope is a slice on them, as an OLAP cube's slice
is.

`work_totals` in `services/work_totals.py` computes it. A read that shows a
record's totals calls it rather than summing a second way.

## A scope names the worklogs a total reaches

| Scope | Reaches |
| --- | --- |
| an idea's number | its own worklogs |
| an issue's number | its own worklogs, and those of every issue under it, at any depth |
| an initiative's number | its own worklogs, the idea it was promoted from, every issue in it, and every issue under those |
| a worklog's name | that worklog |
| `repo` | the worklogs of the issues whose `repo` is that name; an empty name reaches the issues on no repo |
| none | every worklog, and every message no worklog held |

Named together, they narrow together. `slate totals 2072 --repo slate` is the
initiative's worklogs on slate's issues.

- **An issue reaches the issues under it.** A parent is work too large for one
  issue, split into children, so its work is its children's.
- **An initiative walks its issues' children too.** A child does not take its
  parent's initiative. Without the walk, an issue's total could exceed its
  initiative's. A child filed in another initiative then counts in both.
  Totals are groupings, never a partition, as an issue's total is also inside
  its initiative's.
- **An initiative reaches the idea it was promoted from.** Promotion makes the
  idea the initiative, so the goes at the idea are the same work under its
  earlier number.

## A repo's total is its issues' worklogs

A worklog counts toward the repo its record's issue names.

- An issue names its repo from the moment it is filed. So a worklog counts
  from its start, before it has committed anything.
- A worklog sits on one record, and an issue names one repo. So no worklog
  counts in two repos.
- A commit an issue's worklog made in another repo counts in the issue's repo.
  An initiative's own worklogs and an idea's count in no repo.

*Rejected:* the worklogs whose links name the repo. A worklog that has not
committed yet would count nowhere, and one that committed to two repos would
count in both.

## A window narrows each figure to what happened inside it

| Figure | Counted when |
| --- | --- |
| messages and tokens | the message was written inside the window |
| commits, pull requests, landings | the link was made inside the window |
| sessions | the session was in one of the scope's worklogs while the window was open, or spent a counted token |
| worklogs | the worklog was under way during the window, or took a link inside it |

A window is half open, as every bounded read is. `since` is the first instant
inside it, and `until` the first instant past it. A message has one moment, so
an edge that cuts a span splits the span's tokens exactly. A span or worklog
whose claim ran out stopped where the claim did, as every read of one gives.

A link counts once per ref within a kind. A pull request two goes at one issue
both linked is one pull request.

## A session counts where it worked or what it spent

One rule covers every scope. A session counts when it was in one of the
scope's worklogs during the window, or spent a token the total counts.

- For a record, that is the sessions that joined any of its worklogs.
- For a window with no other scope, it is the sessions in any worklog then,
  and the sessions with a message in the window outside every worklog.

A session in a worklog that never reported its tokens still counts, because
it worked there.

## A total naming no record, worklog or repo counts time outside every worklog

A message no span held belongs to its session alone
([sessions.md](sessions.md)). A total that names none of the three counts it,
and `tokens_outside_worklogs` is that part of `tokens`. A total naming any of
them reaches only worklogs, so that field is null there.

## Tokens are counted by kind, and split by model

Each count names one of the four kinds [sessions.md](sessions.md) gives, and
nothing sums across them. `tokens_by_model` gives each model's messages and
tokens. A kind's cost differs by model, and pricing happens outside slate, so
a reader needs the split to price a total. A sum over no message is null,
never zero.

`slate totals --json` prints the API's answer, so it carries every figure the
text prints.
