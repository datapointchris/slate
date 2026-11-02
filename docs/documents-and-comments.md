# Documents and comments

A record of any kind keeps research, design and plan documents, and comments
on the record or on one line of a document. `slate documents` and `slate
comments` are the CLI, and the API serves them under `/api/v1/documents` and
`/api/v1/comments`.

## A document is named on its record, and every write adds a version

`412/plan` is record 412's document named `plan`. Its kind is `research`,
`design` or `plan`, a closed vocabulary that `slate vocabulary` lists. A name
is unique on its record, so a record holds two documents of one kind under two
names. A name that is a kind holds only that kind, so `412/design` is always
a design. Creating it as another kind answers 422. The CLI takes such a name
as its kind, so `slate documents write 412 plan` writes 412's plan, and `slate
documents write 412 cli-plan --kind plan` writes a second plan.

- *Rejected:* one document per kind on a record. A record researched on two
  subjects would hold both in one body.
- *Rejected:* numbering a kind's documents, as `412/plan.2`. It reads as a
  worklog's name, or as a version.

`PUT /documents/{record}/{name}` writes the next version. The write that
creates the document names its kind and answers 201. A later write naming
another kind answers 409, since its body belongs in another document. A body
matching the latest version exactly writes nothing and warns
`document_unchanged`, so repeating a write changes nothing.

That is how S3 keeps a versioned object. Every PUT adds a version, a read
answers the latest, and an earlier one stays readable by its id.
`GET /documents/{record}/{name}?version=N` reads an earlier one here.

A version is never edited. So a document's versions are its history, and a
line comment always names text that still exists.

The body is kept as given. Its lines are what lies between newlines, and a
final newline ends the last line rather than starting another. A version's
read answers them as `lines`, and the CLI prints those. So the number a reader
sees is the one `POST /comments` takes.

## A version and a comment name their writer from the token, and where from by reference

Each version and each comment carries:

| Column | What it says |
| --- | --- |
| `written_by` | the caller's subject or client id, the actor the audit trail records |
| `session_id` | the agent session that wrote it, a row of `agent_sessions` |
| `worklog_id` | the worklog that session was in at that moment |
| `written_ts` | when it was written |

The CLI sends the session from `CLAUDE_CODE_SESSION_ID`. The API finds its
worklog the way `slate worklogs current` does, so a version names the worklog
a commit made at the same moment is stamped with. A session in no live worklog
writes a version naming none, and the answer warns `no_worklog`. A person at a
terminal sends no session, and writes with neither.

A comment's `written_by` is null when its writer was never recorded. A write
through `POST /comments` always records one, and a scoped client writes as its
own client id.

- *Rejected:* a free-text author the client sends. The token already says who
  is calling, and the session and worklog are rows a reader can follow.

A worklog's read lists every version written in it as `document_versions`.
Its stage is `planned` once it wrote a plan, until a link takes it further.
[worklogs.md](worklogs.md) gives the order.

## A comment sits on a record, or on one line of one version

A comment naming no document is on the record. Every kind's read lists them as
`comments`, oldest first, and lists the record's documents as `documents`.

A line comment names a document, a version and a line, and is read with the
document. `POST /comments` takes `document` and `line`, and `version` when the
line is not the latest version's. A line past the version's last answers 422.

`comments` references a line comment's document by the pair of record and
document. So the database refuses a comment on one record naming another
record's document, as each kind's table references `records` on its number and
kind.

`GET /comments?record=N` lists every comment on a record, line comments among
them, oldest first. `DELETE /comments/{id}` answers the comment as it stood.

## A line comment follows its line into a later version until the line changes

A read of any version carries every line comment written on any version. Each
sits on its `shown_line`:

- on the version it was written on, its own line
- on another version, the line its own became, found by diffing the two
  versions' lines
- on none, when that version does not hold its line unchanged

`slate documents show` prints a placed comment under its line, naming the
version and line it was written on when that is another. It prints a comment
on no line after the document, quoting the line it was written on.

A code review carries a comment across revisions the same way. GitHub keeps a
review comment on its line while later pushes leave the line alone, and marks
it outdated once one changes it. Gerrit ports comments onto the next patch
set. A reader of a later version sees which feedback still sits on unchanged
text, and so may not have been taken up.

The diff is `difflib.SequenceMatcher` over whole lines, with its junk
heuristic off so a blank line matches like any other. Only a line inside a
block the two versions share carries over. The placement is derived on every
read and never stored.

- *Rejected:* showing a comment only with the version it was written on. A
  reader of version 3 would read versions 1 and 2 to find feedback that still
  applies.
- *Rejected:* moving a comment's stored line on each write. It rewrites
  history, and a wrong match would lose the line the comment was written on.

## A service comments under slate.comments.write

`slate.comments.write` reaches `POST /comments`, so a job comments on any
record as its own client id. A job closing an issue whose fix merged holds it
beside `slate.issues.close`, which reaches only the close. `slate.records.read`
reaches every read of documents and comments. No scope writes a document or
deletes a comment.

## Deleting a record deletes its documents and comments

The answer names them as `documents_deleted`, listing each document, and
`comments_deleted`, counting every comment, line comments included.
[records.md](records.md) lists what else a delete names. A version or comment
on another record that one of the deleted record's worklogs wrote keeps its
row, and names no worklog.
