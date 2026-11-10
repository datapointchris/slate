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
`slate show <number>` prints any record through its kind's own renderer. Its
`--json` is this envelope, with `record` holding what the kind's own show
prints: an initiative's issues beside it, and the record's changes when
`--changes` is set. `slate show <number>.<seq>` prints a worklog. A
worklog is not a record, so its `--json` is `{kind: "worklog", name, record}`,
named by the name it is read back with, and `slate search --kind` does not
take `worklog`.

## A record's changes are every write to it and to the rows hanging off it

`GET /records/{number}/changes` answers the record's rows of `audit_trail`,
newest first. Each says when it was written, by which actor, to which table
and key, and the columns it changed with their old and new values.
`is_own_row` marks a write to the record's own row, in its kind's table or in
`records`. `limit` keeps the latest.

`slate show` and each kind's own show print the latest changes after the
record, and `--changes` sets how many. A failed read leaves the record printed
and names the failure on stderr. A `--changes` the caller set makes the failure
the command's, because the changes are then what it asked for. `--json` reads
no changes unless `--changes` is set. An insert holds the whole row, so a
record's changes repeat each comment and each document version in full. With
`--changes`, `record` holds `changes` as `{rows, older}`, and `older` says
changes past those shown exist.

A row hangs off a record when single-column `ON DELETE CASCADE` foreign keys
lead to it from the record, through any number of rows: a claim on the
record's number, then a wait on that claim. Deleting the record deletes the
row, so the row is part of it. The read takes that list from the schema's
foreign keys, so a table added later is read with no change to the read.
Three cases are set by hand in `services/record_changes.py`:

- A worklog's spans are left out. Each is a session joining or leaving the
  worklog, and every hold and resume writes several.
- An idea's changes hold the insert of the initiative promoting it, and no
  later write to that initiative. Those are the initiative's own.
- An issue's comments in `issue_comments`, a table imported rows name and no
  model maps, are read by the issue each one names.

An issue moving into an initiative is that issue's change, not the
initiative's. Its key onto the initiative sets null on delete, so the
initiative's deletion leaves the issue standing.

The trigger stores only the columns an update changed, so the update ending a
claim names no record. An insert or a delete holds the whole row, so it names
the row's parent. The read finds each row hanging off the record by the parent
its insert or delete names, then every write to that row by its key. A GIN
index over the `changes` of every insert and delete serves each parent column
through one containment test, `changes @> {"record_number": {"new": 412}}`. So
a table added later needs no index of its own.

Audit rows imported with another tracker's rows keep the table names, keys and
actors they were written with. An issue's own rows match on its UUID, which the
import keeps. Comments on that schema lived in `issue_comments`, keyed by their
own id there. A tracker whose audit trail started after its rows did holds
deletes with no insert before them. The read matches a delete's old parent for
that reason.

- *Rejected:* the changes inside each kind's read. An issue's writes answer
  that read, so every write would read its whole audit trail back.
- *Rejected:* a hand-written list of the tables hanging off each kind. Nothing
  kept it level with the schema, and it missed the initiative a promotion
  creates.

## A search reads every kind and every status, and a number names its record

`GET /records?search=<words>` lists each record whose title or description
holds every word, in any order. A word that is a number, as `2175` or `#2175`,
also matches the record with that number. That record lists first, ahead of the
records whose text cites it, because a reader typing a number is looking for
that record. It reads every status, because the record a reader remembers may
be the one that closed. Unclosed records come next, then the most urgent, then
the newest. An idea has no status, so it sorts with the unclosed. `kind`
narrows the list to one kind. `slate search <words>` prints it. `slate issues
list --search <words>` asks `GET /issues?search=` instead, beside every issue
filter, and matches a number the same way.

## Tags are a short closed list every kind carries, and an issue reads its initiative's

A tag is a category with no end, such as `security`. A tag exists before a
record carries it, created with a one-line description. A write naming a tag
nobody made is refused with 422, naming every tag and the ones near what was
typed:

```text
Unknown tag 'perf'. A record carries only a tag that exists: performance, security. Near what was typed: performance. `slate tags create` adds a tag.
```

So `perf` beside `performance` cannot become a second tag that splits the
records a filter finds.

`tags` holds the list, keyed by an integer `id` with a unique `name`.
`record_tags` holds one row per record and tag, keyed on `(record_number,
tag_id)`, with an `ON DELETE CASCADE` key onto `records.number` and another
onto `tags.id`. It hangs off `records` as claims and documents do, so one
table serves ideas, initiatives and issues, deleting a record deletes its
rows, and `slate show` prints each tagging among the record's changes. A rename
writes the tag's row alone, because a membership names the tag by its id.

An issue reads as carrying its own tags plus its initiative's, derived on every
read as its effective priority is. Its read holds both: `tags` is its own,
which its writes set, and `effective_tags` is the union, which a `tag` filter
matches. So tagging an initiative reaches every issue in it, and moving an
issue between initiatives changes what it reads with nothing stored on the
issue. A finished initiative still lends its tags, because a category stays
true of finished work.

`tag` narrows `GET /ideas`, `GET /initiatives`, `GET /issues` and `GET /queue`.
`tags` on a create sets the record's own, and `tags` on an update replaces the
whole set. A promotion gives the initiative the idea's tags unless the request
names its own. `GET /tags` lists each tag with how many open records of each
kind carry it themselves, and `GET /vocabulary` carries the same list.
Deleting a tag takes it off every record, closed ones included, and the answer
names each with `tag_removed`.

Labels stay apart. They mark how an issue was filed, they reach only issues,
and their groups exclude each other.

- *Rejected:* free-text tags. Nothing stops `perf`, `performance` and `speed`
  from meaning one thing, and a filter on any of them misses the rest.
- *Rejected:* a membership keyed on the tag's name. A rename would rewrite
  every row carrying it, and change the key each row's history is read by.
- *Rejected:* copying an initiative's tags onto its issues. The copy goes stale
  the moment the initiative's tags change or the issue moves.

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
promoted. Each match warns `possible_duplicate`, most alike first, up to
`MOST_NAMED` of them. The record is filed whatever the warning says, because a refusal would
make whoever files it ask again to file work they already checked.

Likeness is the Dice coefficient over the stemmed words of two titles: twice
the stems they share, over the stems the two hold. Postgres' `english` text
search configuration stems and drops stop words, so `transcripts` meets
`transcript` and `the` counts for nothing. A pair at `DUPLICATE_LIKENESS` or
above warns. `services/likely_duplicates.py` holds both values. Two records on
different repos are different work, so they are never compared. An idea or an
initiative names no repo, so it is compared with everything.

The bar was set by replaying each filing in a tracker of about 1,900 issues
against the issues open when it was filed. The figures hold for the stemming
and the Dice measure above, so a change to either moves them:

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
| `dependency_removed` | an issue that depended on the deleted record, or an initiative that waited on it, no longer does |
| `issue_detached` | an initiative's issue is left in no initiative |

It warns `deleted_anothers_claim` when another holder's live claim was on the
record.
