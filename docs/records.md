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
"a record" holds one foreign key to `records`, whatever the kind. A claim does,
and so will a worklog, a document and a comment.

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
issue's parent, its dependencies, its comments. The number is the handle a
person or an agent types.

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

The key holding the record names what was written: `issue`, `initiative`,
`label`, `comment`. A side effect is a change the write made beyond its
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

## Deleting a record deletes its number

The delete endpoints delete the `records` row. Every kind's table references
it with `ON DELETE CASCADE`, so the kind's row goes with it, and with that row
whatever cascades from it.
