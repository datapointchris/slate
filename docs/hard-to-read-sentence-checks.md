# Hard-to-read sentence checks

After slate's API writes a title or summary, it sends the text to a
hard-to-read-sentences service, which scores each sentence. The API never
refuses a write for what the service finds. A person's CLI can rewrite a flagged
title before it runs, but a script or a job cannot. So the check comes after the
write. The service's answer carries an inbox report for a flagged title, and
whatever drains that inbox rewrites the title later.

## Every write path is checked, because the session notes the text

`hard_to_read_sentence_checks/written_text.py` listens on every SQLAlchemy
session, after each flush:

- An issue's or an idea's `title`, and an initiative's `name` and `summary`, are
  checked as `slate.title` and `slate.summary`.
- A field is checked when a flush adds it or changes it. A field set to the text
  it already held is not.
- Once the transaction commits, each record's latest fields go as one check
  that names the record's number. A transaction that ends without its commit
  sends nothing.

So a create, an edit and a promotion each send their text, and a new write path
needs no call of its own. A promotion sends the idea's title again, because the
new record kind writes it as a new row. Only a request that writes attaches a
watch to its session, so a read costs nothing.

Each check names door `slate-api`, the caller's client id, the record's number,
the write's flow, and the agent session that the request's baggage names.

## No request waits on the service

`hard_to_read_sentence_checks/sender.py` queues each check and returns. Two
worker threads post the checks to `POST /find`, and each waits 5 s for its
answer.

*Rejected:* FastAPI's background tasks. A background task runs in the same
thread pool as every `def` handler, so a hung service would hold those threads
while the checks waited. The queue holds 1000 checks. A check that finds it full
is recorded as unanswered at once.

A stopping API sends what it can for 2 s, then records each check still waiting
as unanswered.

## The service records each check it answers, and the API records the rest

The service emits one OTLP log record per field it checks, clean or flagged. A
check can get no answer: a refused connection, a timeout, a non-200 status, or a
body without what the answer schema requires. For each field of such a check,
the API emits one record with `result: unanswered`, the same labels and its
`why_unchecked`. An outage then reads as unanswered checks rather than as clean
text.

The records follow the service's published request schema. Each attribute is the
name that the schema gives its label as `x-attribute`. They export through the
logger provider in `logger.py`, which `docs/tracing.md` covers.

## A flagged title's report is filed under the API's own client

When the service warns about a title or summary, its answer carries a
`flagged-text` report. The API files that report unchanged, as the one element
of a `POST /inbox/reports` body.

- The API signs in with the client-credentials grant, as `FLEET_CLIENT_ID`,
  asking only for the inbox's record scope. pyclilogin's `ServiceTokenSource`
  keeps the token in memory until it lapses.
- A filing is tried three times. A 400 or 403 ends it at once, because a retry
  would be refused the same way.
- A filing that is never kept is logged at error, with the whole report, so it
  can still be filed by hand.

The service files nothing itself. Were it to file under its own client, any
caller that its network admits could make it write.
