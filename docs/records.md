# Records

slate's planning records are ideas, initiatives and issues. An idea is work
that may happen. An initiative is a committed outcome that groups and orders
issues. An issue is one change with its acceptance.

## Every kind is one record, numbered from one sequence

`records` holds one row per number, saying which kind holds it. The number
comes from the Postgres sequence `record_numbers`. Each kind's table,
`ideas`, `initiatives` and `issues`, references its row one to one.

That is class table inheritance: a supertype table with what every kind
shares, and a table per kind with what is its own. Anything that attaches to
"a record" holds one foreign key to `records`, whatever the kind. A claim, a
worklog, a document and a comment each do.

Each kind's table carries its kind beside the number, and the reference is on
the pair. So the database refuses an issue whose number `records` holds as an
initiative's.

- *Rejected:* an exclusive arc, where each attaching table holds one nullable
  foreign key per kind and a `CHECK` that exactly one is set. Every attaching
  table grows a column for every new kind, and resolving a bare number means
  trying each table in turn.
- *Rejected:* one table for every kind. An issue's rank, type, dependencies
  and deferral mean nothing on an idea, so most columns would be null on most
  rows, with a `CHECK` per kind to say which.

Each kind keeps its own UUID `id` as its key, which its own edges use: an
issue's parent, its dependencies, its labels. The number is the handle a
person or an agent types.

`records` also holds `rank`, the place each record takes in the queue, so
every kind is ordered in one space. [issues.md](issues.md) gives the order.

## A number reads as its kind's whole record

`GET /records/{number}` answers `{kind, number, record}`, where `record` is
the read the kind's own route gives, with every claim the record has had:

| `kind` | `record` is what this answers |
| --- | --- |
| `idea` | `GET /ideas/{number}` |
| `initiative` | `GET /initiatives/{number}` |
| `issue` | `GET /issues/{number}` |

`kind` discriminates the union in the OpenAPI schema, so a typed client
decodes `record` into the right shape before reading it. A number nobody
holds answers 404. A number read through another kind's route answers 404
naming the kind that holds it: `issue 1501 not found: #1501 is an initiative`.
`slate show <number>` prints any record through its kind's own renderer, and
its `--json` is this envelope. `slate show <number>.<seq>` prints a worklog. A
worklog is not a record, so its `--json` is `{kind: "worklog", name, record}`,
named by the name it is read back with, and `slate search --kind` does not
take `worklog`.

## A search reads every kind and every status

`GET /records?search=<words>` lists each record whose title or description
holds every word, in any order. It reads every status, because the record a
reader remembers may be the one that closed. Unclosed records come first, then
the most urgent, then the newest. An idea has no status, so it sorts with the
unclosed. `kind` narrows the list to one kind. `slate search <words>` prints
it. `slate issues list --search <words>` asks `GET /issues?search=` instead,
beside every issue filter.

## Every write answers what it wrote and what else it did

A write answers an envelope, never the bare record:

```json
{
  "issue": {"number": 412, "status": "open"},
  "side_effects": [
    {"code": "ancestor_reopened", "message": "Reopened #398, because ...", "record": 398}
  ],
  "warnings": []
}
```

The key holding the record is named for what was written, such as `issue` or
`claim`. A side effect is a change the write made beyond its
request. A warning is a condition the write went through despite. Each entry
carries a `code` a client branches on, a `message` for a person, and the
number of the record it concerns.

So nothing a write changes is left for the caller to discover. A handler takes
the `Effects` dependency from `services/write_effects.py`. It calls `did()` for
each change beyond the request and `warn()` for each condition, then answers
through `effects.answer()`. `WriteEffectCode` holds every code.

The CLI prints the record's line, then each side effect indented under it.
Warnings go to stderr as `Warning: <message>`, under `--json` too, and
`--json` prints the whole envelope.

This is the mutation payload GraphQL APIs answer with, and the stderr warnings
kubectl prints from the API server.

- *Rejected:* response headers, which is where Kubernetes sends warnings. A
  header holds a string, so a client would parse the record number back out
  of the message.
- *Rejected:* a log line alone. The caller never sees it.

## A new record reading like an open one files anyway, with a warning naming it

Creating an issue, an initiative or an idea compares its title with every
open record's: unclosed issues, initiatives not finished, and ideas not
promoted. Each match warns `possible_duplicate`, most alike first, at most
three. The record is filed whatever the warning says, because a refusal would
make whoever files it ask again to file work they already checked.

Likeness is the Dice coefficient over the stemmed words of two titles: twice
the stems they share, over the stems the two hold. Postgres' `english` text
search configuration stems and drops stop words, so `transcripts` meets
`transcript` and `the` counts for nothing. A pair at 0.45 or above warns. Two
records on different repos are different work, so they are never compared. An
idea or an initiative names no repo, so it is compared with everything.

The bar was set against a tracker of about 1,900 issues, replaying each filing
against the issues open when it was filed:

- Of four pairs labeled duplicates, two shared their wording and scored 0.48
  and 0.57. The other two were worded apart, at 0.15 and 0.26, so no bar on
  words reaches them.
- At 0.45, about 7% of filings warn when only the same repo is compared, and
  about 13% across every repo. At 0.50, about 5% and 10%.
- Titles built from a template, such as a series of guides each naming its
  subject, are the floor of that noise.

- *Rejected:* trigram similarity, `pg_trgm`. It needs an extension, and its
  scores separated the labeled pairs from the rest no better.
- *Rejected:* matching every word, as search does. A reworded duplicate shares
  most words but rarely all.
- *Rejected:* embeddings. Two of the four labeled pairs need meaning to match,
  and slate holds no model to embed with.

## Deleting a record deletes its number

The delete endpoints delete the `records` row. Every kind's table references
it with `ON DELETE CASCADE`, so the kind's row goes with it, and with that row
whatever cascades from it.

A delete answers the envelope too, with the record as it stood under `deleted`.
Its side effects name what went with it and each other record it changed:

| Code | When |
| --- | --- |
| `claims_deleted` | the record had claims, live or ended; they go with it |
| `worklogs_deleted` | the record had worklogs; they go with their spans and links |
| `documents_deleted` | the record had documents; they go with every version |
| `comments_deleted` | the record or its documents had comments; they go with them |
| `child_detached` | an issue's child loses its parent |
| `dependency_removed` | an issue that depended on the deleted one no longer does |
| `issue_detached` | an initiative's issue is left in no initiative |

It warns `deleted_anothers_claim` when another holder's live claim was on the
record.
