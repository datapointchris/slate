# Worklogs

A worklog records one go at one record: the agent sessions that worked it and
when, the commits, pull requests and landings it produced, how the work was
approached, and how the go ended. `slate worklogs` is the CLI, and the API
serves it under `/api/v1/worklogs`.

## A worklog is named for its record and its place among the record's goes

`1991.2` is the second go at record #1991. The sequence counts per record from
one, so the name says what was worked and how many times before. The API takes
the name wherever it takes a worklog: `GET /worklogs/1991.2`. `slate show
1991.2` reads it as `slate show` reads any record.

A worklog is a row of `worklogs` on a record number, like a claim. Its spans
are rows of `worklog_spans`, and its links rows of `worklog_links`.

## A hold opens one, and the claim's ending ends it

Two things open a worklog:

- **A hold.** Each claim opens its own worklog, and the hold's answer names it
  as `worklog_opened`. Every ending of the claim ends the worklog at the same
  moment with the same outcome. [claims.md](claims.md) covers those endings.
- **`slate worklogs start <record>`**, for a go no hold opens, such as
  discussing an idea, or the next step of a record the session already holds.
  Each worklog already open on the record goes through with an `open_worklog`
  warning, since joining it may be what was meant.

A claim is on one worklog at a time, and follows the session that holds it.
Where that session starts or joins another worklog on the record, the claim
moves to it, and the answer names that as `claim_moved_to_worklog`. Joining
moves it even when the session was in that worklog already. So a holder that
finishes its preflight worklog and starts one for `build` keeps its claim, the
claim's `worklog` names the build's, and releasing the claim ends that one. The
worklog the claim left keeps its state. One still open ends by hand or with its
record, as one `start` opened does.

A worklog's `outcome` says which ending it reached:

| Outcome | Means |
| --- | --- |
| `finished` | `slate worklogs finish` ended it by hand |
| `released` | its claim was released |
| `expired` | its claim's lease ran out, and it ended at the claim's `expires_ts` |
| `completed`, `canceled`, `dropped` | the record reached that closing status |
| `promoted` | the idea became its initiative |

Closing or promoting a record also ends each worklog `start` opened on it.
Finishing a worklog whose claim still holds goes through with a
`claim_still_held` warning, and the claim keeps holding. Finishing one that
already ended changes nothing and warns `worklog_already_ended`.

A worklog's `state` is derived on every read, and takes a claim's three names:

| State | Means |
| --- | --- |
| `live` | unended, and its claim, if it has one, is live |
| `expired` | unended, and its claim ran out; the holder extending the claim makes it live again, and any other write that finds the claim ends both |
| `ended` | `ended_ts` is set, or its claim ended without ending it |

`models.Worklog` holds each as a predicate that reads the same in Python and in
SQL, so a read and a list filter cannot disagree.

## A session is in one worklog at a time

A span says an agent session was in a worklog from `started_ts` to `ended_ts`.
A partial unique index allows one open span per session. Putting a session in
a worklog ends its open span anywhere else, and the answer names that as
`session_left_worklog`. The worklog it left keeps its state, and its claim, if
it has one, keeps holding.

So a session holding a claim on one issue, then another, leaves two worklogs.
Each lists the session with its own span, and the spans never overlap. The
second hold moved the session. `slate claims resume` on the first claim, or
holding its record again, moves it back and names that as
`session_rejoined_worklog`. `slate worklogs join <name>` moves it into any live
worklog.

Time a session spends outside any span belongs to the session alone. So
anything measured over a session, such as its tokens, splits at its span
boundaries with each moment counted once. A worklog's read sums the tokens its
sessions reported inside its spans, and [sessions.md](sessions.md) covers how
each message reaches its span.

This is the single running timer that time trackers such as Toggl Track and
Harvest use: starting one timer stops the one running.

- *Rejected:* a span in every worklog whose claim the session holds. The spans
  would overlap, so a token spent while holding two claims would need an
  arbitrary split or count twice.
- *Rejected:* refusing a second hold while the session is in a worklog. A
  session that parks one claim while it waits on a merge holds the next.

A span in a worklog whose claim ran out ends at the claim's `expires_ts`, not
when a later write noticed.

## A commit reaches its worklog through two git hooks that call slate alone

`slate worklogs current` prints the name of the worklog the session is in, or
nothing at all, and exits 0 either way. A `commit-msg` hook stamps the commit
with it as a trailer:

```sh
#!/bin/sh
case $(git remote get-url origin 2>/dev/null) in file:*) exit 0 ;; *:*) ;; *) exit 0 ;; esac
name=$(slate worklogs current) && [ -n "$name" ] &&
  git interpret-trailers --in-place --if-exists doNothing \
    --trailer "Worklog: $name" --trailer "Agent-Session: $CLAUDE_CODE_SESSION_ID" "$1"
exit 0
```

`--if-exists doNothing` keeps an amend from stamping a second trailer. A
repository whose `origin` has no host asks slate nothing, so a test suite
committing in fixture repositories reaches no worklog.

A hook run at `post-commit` and `post-merge` records the commit on the worklog
the session is in as the commit is made, named by its full sha and the
repository its `origin` names. A merge commit runs `post-merge` and never
`post-commit`, so it listens on both. `post-merge` also runs after a squash,
which passes it 1, and after a fast-forward, whose reflog subject ends
`: Fast-forward`, and neither makes a commit, so neither links one. A commit
made outside any worklog links nothing and prints nothing. A rebase makes new
shas, and each links to the worklog of the session that ran it. `--session`
defaults to that session, and the link's `linked_by` names who ran it:

```sh
#!/bin/sh
origin=$(git remote get-url origin 2>/dev/null)
case $origin in file:*) exit 0 ;; *:*) ;; *) exit 0 ;; esac
[ $# -eq 0 ] || { [ "$1" = 0 ] && ! git reflog -1 --format=%gs HEAD | grep -q ': Fast-forward'; } || exit 0
name=$(slate worklogs current) && [ -n "$name" ] || exit 0
slate worklogs link "$name" --commit "$(git rev-parse HEAD)" --repo "$(basename "$origin" .git)" >/dev/null
```

`current` reads the session from `CLAUDE_CODE_SESSION_ID`, or `--session`. With
no session, it prints nothing without asking the API. A repository whose
hooks a manager such as pre-commit installs adds these two through it, since
its install replaces `.git/hooks`. A hook declared in git config elsewhere can
run the same two commands. The trailer is the commit's own record of its
worklog, readable with `git log` and no slate. The link is slate's record of the
commit, readable with no clone. Gerrit's `Change-Id` hook stamps a commit the
same way.

## Links record what a worklog produced, and are never followed

A link is a `commit`, a `pull_request` or a `landing`, with the ref as given
and the repository it is in. slate stores the ref and never resolves it. A
worklog that ended still takes a link, since a pull request lands after the
claim that made it is done. Linking the same ref twice changes nothing and
warns `already_linked`.

A worklog's `stage` is the furthest it reached, derived on every read:
`started`, then `planned` once it wrote a plan document, then `committed`,
`pull_request` and `landed` as its links reach them. Its read lists every
document version written in it as `document_versions`.
[documents-and-comments.md](documents-and-comments.md) covers how a version
names its worklog.

## A worklog names the step of its record's life it performed

A worklog's `step` is one of the rows of `worklog_steps`, such as `design`,
which `slate vocabulary` lists in `position` order with each description.
`slate claims hold <record> --step <step>` sets it on the worklog the hold
opens, and `slate worklogs start <record> --step <step>` on one with no claim.
`slate worklogs edit <worklog> --step <step>` corrects it, and `--step ""`
clears it. No step is required. Both writes refuse a field they do not name
with a 422, so a client sending one learns it was not applied.

A hold of a record its holder already holds opens no worklog, so it keeps the
step the claim's worklog has. Where that worklog is open and carries the step
asked for, the `already_held` warning says so. Where it names no step, the
warning names the edit that sets it. Where it carries another, or has ended,
the warning names `slate worklogs start <record> --step <step>`, which opens a
worklog of that step for the claim to move to.

A step is checked on a record once a worklog of that step ended because its go
did: finished by hand, released, or ended by the record completing or the idea
being promoted. A claim that ran out ended nobody's go, and a record dropped or
canceled abandoned it, so `expired`, `dropped` and `canceled` check nothing.

An initiative's read derives `steps`, each checked step from its latest such
worklog: the worklog's name, when it ended, and the first session in it. It
reads the steps of the idea promoted into it too, because an idea is shaped
before it has an initiative's number. It derives `stage` as well, the furthest
checked step, or `idea` before any. [issues.md](issues.md) covers how the
initiative reads print them. Nothing is read from a document's text, so a retro
document checks `retro` only through a worklog of that step.

A step the initiative marks as not planned is a fact apart from its checks.
`shape`, `design` and `retro` can be marked, and `preflight` and `build` never
can. Marking one never unchecks it, and a worklog of a marked step still checks
it. [issues.md](issues.md) covers the marks and how `next` skips them.

## A preflight worklog holds the token estimate of its record's work

A worklog holds an estimate of the tokens its record's work will spend, by the
kinds [sessions.md](sessions.md) counts. Each kind is its own nullable column,
`estimated_<kind>_tokens`, so null is no estimate of that kind, and differs from
an estimate of zero. A worklog's read carries them as `estimate`, with each kind
null where it was not estimated, and `estimate` itself null where no kind was.

`slate worklogs edit <worklog> --estimate-output 900000` sets one kind and keeps
the rest. `--estimate-uncached-input`, `--estimate-cache-write` and
`--estimate-cache-read` set the other three, and an empty value clears its kind.
The API's `PATCH` takes `estimate` with the kinds to change, a kind named null
clears it, and `estimate: null` clears every kind.

[totals.md](totals.md) reads the estimate from a worklog whose step is
`preflight` alone. So an edit that leaves an estimate on a worklog of any other
step goes through with an `estimate_off_preflight` warning naming the edit that
makes it one.

The estimate is a row rather than text in the plan document, because nothing
in slate reads a document's text.

## A service writes worklogs under its own client id

`slate.records.read` reaches every worklog read. `slate.worklogs.write`
reaches starting, joining, linking, editing and finishing. A scoped client
starts and links only as its own client id. It joins, edits and finishes only
a worklog that id opened, because joining another's would stamp that session's
commits onto it. A hold's worklog comes with the hold, under
`slate.claims.hold`.

`slate.worklogs.observe` reaches joining alone, and joins any live worklog. It
is for a session that commits nothing, such as a pull request's reviewer. Such a
session has no commit to stamp, and joining is how its tokens land on the work
it served. Its token cannot start, link, edit or finish a worklog.

Deleting a record deletes its worklogs with their spans and links, and the
answer names them as `worklogs_deleted`.
