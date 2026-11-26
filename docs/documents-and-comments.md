# Documents and comments

A record of any kind keeps versioned documents, of the kinds `slate
vocabulary` lists, and comments on the record or on one line of a document.
`slate documents` and `slate comments` are the CLI, and the API serves them under
`/api/v1/documents` and `/api/v1/comments`.

## A document is named on its record, and every write adds a version

`412/plan` is record 412's document named `plan`. Its kind, such as `plan`,
is one of a closed vocabulary that `slate vocabulary` lists. A name
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
another kind answers 409, since its body belongs in another document. A write
whose body and `fields` both match the latest version's writes nothing and
warns `document_unchanged`, so repeating a write changes nothing. A change to
either one writes a version, and so does leaving out `fields` that the latest
version holds.

That is how S3 keeps a versioned object. Every PUT adds a version, a read
answers the latest, and an earlier one stays readable by its id.
`GET /documents/{record}/{name}?version=N` reads an earlier one here.

A version is never edited. So a document's versions are its history, and a
line comment always names text that still exists.

The body is kept as given. Its lines are what lies between newlines, and a
final newline ends the last line rather than starting another. A version's
read answers them as `lines`, and the CLI prints those. So the number that a
reader sees is the one `POST /comments` takes.

## A version carries structured fields beside its body, for a program to read

A version's `fields` is a JSON object, or null where the write sent none. It
holds what a program reads off the document, such as a preflight's verdict, so
no reader parses the body's prose for it. `slate documents write --fields
<file>` sends a file's object. The API answers 422 to anything but an object,
and checks nothing inside it, so each kind of document sets its own keys.

`fields` comes back on every read of a version:

| Read | Where |
| --- | --- |
| `slate documents show <record> <name> --json` | `version.fields` |
| `slate documents list --json` | each document's `latest.fields` |
| a worklog's read | each entry of `document_versions` |

An API one release older drops `fields` and still answers 200. So the CLI
compares the version it gets back with what it sent, and fails with `slate's
API predates document fields` where they differ.

## The list bounds records rather than rows, so the bound never half lists a record

`GET /documents` answers each document with its latest version and its
`record`: the record's number, kind, title and status. A client groups the
list under its records with no read per record. A record's own read lists its
documents without `record`, since the record is the read.

The list puts the most recently written document first, so one record's
documents interleave with another's. A row cap would cut through a record and
drop its older documents. So `record_limit` bounds the list instead. It ranks
each record by its latest document write, and keeps every document on the
newest that many. `RECORD_LIMIT_DEFAULT` and `RECORD_LIMIT_MAX` in
`services/row_limit.py` set its default and its maximum. `limit` still cuts
rows after it, and can cut a record short.

`X-Total-Records` counts every record holding a matching document. A client
tells a short page from the whole by it. `slate documents list
--records <n>` sends the bound, and says on stderr where records went unshown.
The list has no cursor, so a record past the maximum is read only by naming it.

- *Rejected:* an envelope holding the rows and the total. Readers decode the
  body as a bare array, and an envelope would break each one on the next
  deploy.

## A version and a comment name their writer from the token, and where from by reference

Each version and each comment carries:

| Column | What it says |
| --- | --- |
| `written_by` | the actor the audit trail records: a person's username, else subject, or a service's client id |
| `session_id` | the agent session that wrote it, a row of `agent_sessions` |
| `worklog_id` | the worklog that session was in at that moment |
| `written_ts` | when it was written |

The CLI sends the session from `CLAUDE_CODE_SESSION_ID`. The API finds its
worklog the way `slate worklogs current` does, so a version names the same
worklog as a commit made at that moment. A session in no live worklog
writes a version naming none, and the answer warns `no_worklog`. A person at a
terminal sends no session, and writes with neither.

A comment's `written_by` is null when its writer was never recorded. A write
through `POST /comments` always records one, and a scoped client writes as its
own client id.

- *Rejected:* a free-text author that the client sends. The token already says
  who is calling, and the session and worklog are rows a reader can follow.

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
block that the two versions share carries over. The placement is derived on
every read and never stored.

- *Rejected:* showing a comment only with the version it was written on. A
  reader of version 3 would read versions 1 and 2 to find feedback that still
  applies.
- *Rejected:* moving a comment's stored line on each write. It rewrites
  history, and a wrong match would lose the line that the comment annotated.

## A service comments under slate.comments.write and writes documents under slate.documents.write

`slate.comments.write` reaches `POST /comments`, so a job comments on any
record as its own client id. A job closing an issue whose fix merged holds it
beside `slate.issues.close`, which reaches only the close.
`slate.documents.write` reaches `PUT /documents/{record}/{name}`, so a job
writes a document's next version as its own client id. `slate.records.read`
reaches every read of documents and comments. No scope deletes a comment.

## Deleting a record deletes its documents and comments

The answer names them as `documents_deleted`, listing each document, and
`comments_deleted`, counting every comment, line comments included.
[records.md](records.md) lists what else a delete names. A version or comment
on another record that one of the deleted record's worklogs wrote keeps its
row, and names no worklog.
