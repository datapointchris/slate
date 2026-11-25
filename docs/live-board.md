# A live board of the initiatives

`slate tui` is one program with a tab per view, drawn with the
[gotermboard](https://github.com/datapointchris/gotermboard) library. Its first tab
answers two questions at a glance: what is moving, and what is stuck. This page says why
it has the shape it has. `docs/issues.md` covers the reads under it.

## `tui` sits at the root, and `list` stays a list

No one noun owns every view, so the command takes none. `slate initiatives list` prints
and exits the same way wherever it runs. Format may follow the terminal, as color and
clipping do. What the command does never does, because a script and a person should not
get two different programs from one command line. Its `--help` points here.

- *Rejected:* `list` going live on a terminal, for that reason.
- *Rejected:* `slate views <name>`. Once there are tabs, no view needs a name to start it.

`--view` opens at a tab, and `--once` and `--json` read one, as `k9s -c pods` opens a
one-program TUI at a view. A flag shows in `--help`, where an optional argument does not.
With no terminal and neither flag the command refuses and names both, so an agent reads what
a person reads rather than waiting on a screen that never draws.

## Each initiative is two lines: its name, then dim repos beside its counts, and a third once a step is checked

```text
                                    DONE  24H   7D  30D  LAST  RUN  BLK  RDY  ISSUES
   TOTAL · 85                   588/1175   27  125  372   16m   23  207  281  ▰▰▰▰▰▰▰▰▰▰▱▱▱▱▱▱▱▱▱▱ 50%  ▅ ▂▁

 ▾ high · 5 of 50                453/786   24   87  294   16m   22  135  128  ▰▰▰▰▰▰▰▰▰▰▰▰▱▱▱▱▱▱▱▱  ▆ ▂▂
▌  ▸ 1544  An agent keeps the queue ordered toward its goals as proposals a person steers
           api, cli, docs, +6      19/48    9   11   19    3h   14    8    7  ▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱
```

A number and a name are what a person scans for, so they lead, the name in bold, and a name wraps
under itself rather than being cut. Columns let the eye run down one figure across every initiative,
and a tier's totals line up over the rows they sum. Repos are read when acting on a row rather
than when scanning, so they stay dim in the first column and end in `+N`, because a repo cut in
half names nothing. The side panel lists them all, and the filter matches every one.

An initiative that has a checked step draws a third line marking each step of its life, a
checked one green with the day its worklog ended and the rest dim, as `slate initiatives list`
marks them. An initiative that no step reached stays two lines, so marks cost screen rows only where
a step was checked. The side panel lists each checked step with its worklog, when it ended and
the first session in it.

- *Rejected:* three lines, keeping every repo on one of its own. Repos were the loudest line on
  the board, and a screen holds a third fewer initiatives.
- *Rejected:* a first line of words, `19 in 24h · 3 running`. It reads well once and cannot
  be compared down the screen.
- *Rejected:* name and repos on one line. Either one long value pushes the other off.

**The columns count what each read means, and nothing is derived twice.** DONE is completed
over completed and open, because `issue_count` includes the canceled. RUN adds running to
waiting, because the bar draws both yellow. 24H, 7D and 30D count completed issues only,
since the question is how much work finished. A zero reads `·`, so the numbers that are
there stand out.

## A claimed row names its holder, and `s` can put the claimed work first

An initiative or issue someone holds names them in yellow after its title, `held by <holder>`,
as running work is yellow on the bar. So who is on what reads down the screen without moving the
cursor, and the side panel adds when the claim expires and what it waits on. The board read
carries each initiative's claim and each issue's holder, so the rows need no second read.

`s` cycles the order within a tier between priority, latest close, most open, newest and
claimed. Claimed puts the initiatives that someone holds first, then those with the most claimed
issues, which are the running and waiting ones.

- *Rejected:* a claimed grouping on `v`. A sort keeps the tiers, so claimed work still reads
  against the priority it sits at.

## A bar has one cell per issue

`19/48` draws 48 cells through gotermboard's `TallyBar`: done in green, running or waiting
in yellow, blocked in red, and the rest plain. Without color only done is filled and blocked
is marked, so no unfinished issue reads as done.
A bar of fixed width makes a 4-issue initiative look as big as a 67-issue one, and one
blocked issue in 60 rounds away. So one cell per issue keeps size and every state visible,
at the cost of a long bar. A bar wider than its row wraps at its column and keeps every
cell. Canceled issues stay off it, and the panel counts them.

The total and tier rows hold hundreds of issues, so theirs are scaled to a fixed width. A
kind holding any issue keeps at least one cell, and they add a 14-day spark line of closes.

## The client cuts the scope, because the totals need every initiative

`1` shows the first 5 of each priority tier, `2` the first 20 in list order, and `3` all of
them. `+` and `-` change the count. `GET /initiatives/board` has no limit, because its
totals and tiers roll up every initiative whatever shows. So a frame always holds all of
them, and those past the scope are marked to draw only while a filter is typed. That is what
lets `/` find an initiative the scope cut.

Two reads keep this cheap enough to poll. The board reads every 15 seconds and carries no
descriptions, which are most of what an initiative weighs. Long text reads every 60 seconds.
Reads run at their own pace, so a failed read leaves the last good one drawn with its age,
and a rollout never blanks the screen. Every request runs under the API client's `Timeout`,
`apiRequestTimeout` in `cli/internal/cli/client.go`. So when the API never answers, the read
fails like any other rather than holding its source.

**What only one row shows is read for that row alone.** An initiative's next issues and its
tokens by kind come from `GET /initiatives/progress`. An issue's whole description and its pin
come from `GET /issues/{id}`, and its worklogs from `GET /worklogs?record=`. Reading those for
every row is what the board read exists to avoid. So
the read follows the cursor, on the next tick after it lands, which means holding a key down
reads only the row it stops on. A row read once draws at once when the cursor returns.

## Opening a row uses separate keys

`enter` unfolds an initiative's issues, and an issue's sessions and pull requests. `d` opens
an initiative's or an issue's description, and `f` adds the initiative's summary and each
issue's first paragraph. Each answers a different question, so one key that cycled through them
would make the reader step past two answers to reach the third. Unfolded issues go open first,
running at the top and then in queue order, with `done` and `canceled` folded beneath.

A description that fits opens under its row. One that runs past the screen opens on a full-screen
reader, where `[` and `]` jump between its headings, `/` searches it, and `esc` returns to the row.

From 160 columns a side panel follows the cursor, with the summary, the repos, the closes, the
burn-up, the start of the description, what is next and the tokens, so a wide terminal answers
without a key. `p` hides it.

## An initiative's panel draws its burn-up, so scope added under it shows

The bar is a ratio, so an initiative that gains fifteen issues in a day reads as falling behind
rather than growing. The panel draws two lines over the initiative's whole life: its issues, and
its completed ones in green, as done is green on the bar. Scope added steps the upper line up,
and work done lifts the lower one toward it. Each step is drawn square, so a jump reads as one
moment rather than a ramp. The lines come from the progress read's `burnup`, which the cursor's
row already reads, and draw in braille through gotermboard's `chart` package.

## The repos tab answers how fast each repo closes work and how long ready work has waited

On the initiatives tab a repo is dim text beside an initiative's counts, so a cursor has nowhere
to land on one. The repos tab is a line per repo on the same columns, then `WAIT`, the longest
any of its ready issues has waited, and a spark of its closes in each of the weeks that the API
answers. It opens on the most open, and `s` cycles to most ready, longest wait, latest close and
name. A repo with nothing open and no close in those weeks folds under `quiet`. The issues on no
repo are a row of their own, counted apart from the repos.

Its panel draws two charts. Closes per week are columns over those weeks, so a pace that dropped
shows. Ready issues are a histogram of how long each has waited, its spans turning warmer as the
wait grows. A queue that is old rather than long carries its weight on the right. The ones that
have waited longest follow, by title. The total row's panel draws every repo's, each issue
beside its repo.

A week's column can be only a few cells wide on a narrow panel, so each is labeled by its
Monday's day of the month, by the month where the month turns, and the week of the read as
`now`. A chart's value axis marks its top and its middle, and a middle that falls between two
counts is left unlabeled rather than rounded.

A CLI released ahead of its API says so where a chart would be: the repos tab names the missing
read, and the burn-up says the API answers none.

## The ideas tab lists the open ideas newest first, narrowed in the client from one read

```text
slate · ideas   21:32:57   3 open · newest first   pursuit all  tag all   read 0s ago

   3 ideas  PRIORITY  CREATED     PURSUIT          TAG             REFS  TITLE
▌ ▸4164     none      2026-11-22  —                —                  ·  Dated reminders
  ▸4089     high      2026-11-21  Grow the garden  outdoors, food     1  Compost the kitchen scraps and a very long tit…
  ▸3700     medium    2026-11-18  —                —                  ·  A shelf for the library books
```

An idea is work that nobody has taken on, so the tab answers two questions: what was thought of
lately, and what sits near it. Newest first answers the first. `u` narrows to one pursuit and `#`
to one tag, each cycling back to `all` after the last, which answers the second. An idea that
ended is not on the tab, because its work is the initiative's or issue's that `became` names,
and the other tabs draw that.

The tab reads `GET /ideas` whole in its default `open` state, at the pace of the board's long text, because descriptions are
most of what an idea weighs. It narrows in the client by each idea's own `pursuits` and `tags`.
For an idea those lists are the whole membership that the route's `pursuit` and `tag` filters
test, so the client derives nothing that the read does not carry. The same read gives the
choices that `u` and `#` cycle through, every pursuit and tag that an open idea has. So a
key narrows at once.

- *Rejected:* a read per narrowing through the route's filters. A narrowed read cannot list the
  pursuits outside it, so the choices would need a second read. Each key would also draw the last
  narrowing's rows until its own read landed.

A title is cut at the screen's edge, and `f` shows a cut one whole under its row. `enter` opens
the idea's read: its title, priority and days, its pursuits and tags, its refs oldest first, and
its description rendered. `/` matches the description too. `--json` prints what
`cli/internal/cli/tui-ideas.schema.json` describes. A row carries the fields that
`slate ideas list --json` gives the same idea, and `ref_count`, the count that the row draws.

## The documents tab groups every document under its record and opens the latest version

```text
slate · documents   43 records · 183 documents · newest written first   read 0s ago

 DOCUMENT          KIND    VER  WRITTEN  WORKLOG  COMMENTS
────────────────────────────────────────────────────────────────────────────────────────────
 ▾ 212  Every outside source enters through one ingestion layer
▌   plan-review    review    2  19:18    212.3    ·
    plan           plan      1  19:18    212.3    ·
    design         design    8  19:13    212.3    ·
    design-review  review    2  18:16    212.2    ·
 ▸ 240  Reminders fire on a date or on a checked condition  design · plan · review
 ▸ 251  Compost the kitchen scraps  design
```

A document is what a step of the work wrote down: a design, a plan, a review or a retro. At a
review point the question is what was just written. So the record written to last comes first,
and within a record so does its document written last. The first record opens, and the rest fold
to their titles. A folded record names its documents' kinds past its title, each kind once, in
the order of its newest write. So a folded record still says which steps wrote there.

**The read is bounded by record, never by row.** The tab reads `GET /documents` with a
`record_limit` on the long-text poll: every document on the records written to last. The list
interleaves records by each document's latest write, so a cut by row would draw a record with
its older documents missing. `+` and `-` move the bound by `documentRecordsStep`, up to
`api.RecordLimitMost`, the most that one read answers. The API owns that cap. Its suite writes it
to `tests/api/testdata/cli-vocabularies.json`, and the CLI's suite holds the copy level.
`X-Total-Records` counts every record holding a document. So where the bound cut the read, the
strip names the records read of the total, and how many more `+` reads.

- *Rejected:* `limit` on the list read. A row cut drops a record's older documents.

**A body is read again only on a new version or a new comment.** The list carries each
document's latest version and its comment count, and no body. The cursor's document is read
whole with `GET /documents/{record}/{name}` once the cursor lands on it, and the body is kept by
address. A list read showing a newer `latest.version` or another `comment_count` for the cursor's
document reads it again on the next tick. A line comment adds no version, so the count is what
shows one written since. Otherwise the poll reads one list and no body. The read waits for the
next tick, so holding `j` down reads only the document it stops on. A failed body read says so
where the body would be.

**`enter` opens the latest version full screen**, rendered on the board's markdown reader, and
the side panel draws it beside the table where the screen is wide enough. Line comments follow
the body as a list, each naming its line and quoting it. Rendered markdown reflows, so a comment
pinned beside a raw line number would sit beside the wrong text. A comment on a line that the
latest version changed names the version and line where it was written. No earlier version and
no diff show, because a reader here acts on what a document says now. `slate documents show
--version` prints an earlier one.

`/` matches a document's name and kind, and its record's title, so a title finds the documents
inside a folded record. The tab declares no kinds. Each row names its record, so `t` lands on the
record's own tab. A retro sits on a completed initiative, so its `t` widens the initiatives tab
to every status, and `esc` narrows it back.

An API older than the `record` on each listed document answers no `X-Total-Records`. The tab
then names the read it lacks where the rows would be, rather than drawing documents that it
cannot group. `--json` prints what `cli/internal/cli/tui-documents.schema.json` describes: each
record with its documents, under the names that `slate documents list --json` gives them, then
the cursor's latest version with its line comments.

## The pursuits tab is the focus's standing, with each pursuit's usage charted by day beside its share

```text
slate · pursuits   focus 4 · through 12-23 · Lock the doors up front, then finish the warehouse   read 0s ago

 #            SHARE  USAGE  FACTOR  OUTPUT  INIT  IDEAS  LAST WORKED       NAME
   TOTAL · 5                          1.2M               09:00
───────────────────────────────────────────────────────────────────────────────────────────
 ▾   named shares · 2
▌   2984      0.45   0.08   0.88    125.3k     4      0  09:00             Lock the doors
    2983      0.25   0.07   0.82    105.3k     3      1  09:00             Finish the warehouse
 ▾   the rest share 0.30 · 3
    2985      0.15   ·      1.00         ·     1      0  2026-11-21 19:56  Tidy the shed
    2989      0.15   0.65   0.05    704.0k     2      0  09:00             Talk to the neighbors
    2992      ·      0.20   ·       298.6k     0      0  2026-11-23 23:07  Paint the fence
```

A focus gives each pursuit a share of the work, and the tab answers whether the work is going
there. It reads `GET /focus` every 15 seconds, the standing that `slate focus show`
prints, and keeps its order: the furthest under its share first. The rows split by `share_named`
into the shares the focus named and the rest, which take an even part of what the named shares
left. The client computes no factor and fills no share, so the tab and `slate focus show` never
disagree. With no focus in force, one group holds every pursuit and SHARE and FACTOR read `·`.
Every factor is then null, so the standing's order puts the least output first.

`GET /pursuits`, `GET /focus/history` and `GET /focus/usage` read once a minute. The pursuits
read gives each row its summary, its measure, and INIT and IDEAS, which count every initiative
and idea in it whatever its status, as `slate pursuits list` does. It reads every status, because
a pursuit that is not active stays in the standing while its window holds its output. Each is
light enough to poll once a minute, as `tab-prior-art-and-read-costs.md` measures.

**The panel charts the pursuit's usage on each day beside its share.** `GET /focus/usage`
answers every day of the focus from its first to its last, with each pursuit's figures over the
window ending with that day. Each day's usage draws at the end of its window, so the last day's
sits at the read. The share is dotted and runs on to the focus's last day, so the days still to
come show what the work is aimed at. Where a day lacks the pursuit, the line breaks rather than
joining across it, since a pursuit that is not active is listed only while its window holds its
output. A failed usage read says `usage by day unread` where the chart would be. A read that
fails after a good one keeps drawing the good one, and says so under it.

**The total's panel stacks the named shares and the rest.** Usage sums to 1 on a day with any
output, so the stack's height is the whole of the work and each band is a pursuit's part. The
legend names each named pursuit with its share. With no focus naming any, the five used most on
the last day stand apart from the rest. The panel also names the output that reached no pursuit.

- *Rejected:* a line per named pursuit beside its share. Three pursuits and the rest draw eight
  lines that cross.

**`h` and `l` move between focuses.** An earlier focus's rows come from its usage read's last
day: its shares, and its usage, factor and output at the end of that day. The strip names its
span, how it ended and its statement. A focus longer than the API's `USAGE_DAYS` answers its last
`USAGE_DAYS`, and the strip and the chart's heading name the cut from the read's `days_in_focus`,
as `the last 30 of its 45 days`. INIT, IDEAS and LAST
WORKED stand as they are now, because no read answers them for an earlier day. `l` from a focus
that the history no longer lists returns to the one in force.

The tab declares the `pursuit` kind, so `t` from an initiative or an idea lands on its pursuit's
row. A line on another tab names a pursuit as it serves the work now, so a jump shows the focus
in force. An API older than `/focus/usage` answers it as no route. The tab then names the read it
lacks under the total, and the rows still draw the standing. `--json` prints what
`cli/internal/cli/tui-pursuits.schema.json` describes: each row's figures under the names that
`slate focus show --json` gives them, its group, and each day that lists it under the names that
`slate focus usage --json` gives them.

`slate focus usage [--focus <id>]` prints the same read: the window, then a row per pursuit in
the last day's order, with that day's share, usage and factor and a glyph a day.

## The worklogs tab reads the live and expired worklogs every 15 seconds and the newest ended once a minute

```text
slate · worklogs   3 live · 1 expired · newest 100 of the ended   read 2s ago

 WORKLOG     STEP       STAGE      STARTED           SESS  HOLDER            WAITS ON                        RECORD
 ▾   live · 3
▌  ▸ 240.2   preflight  planned    13:46                2  cda42040          the user reading its work plan  Reminders fire on a date…
   ▸ 212.3   design     committed  14:26                4  5a6e9b8b          round 4 on owner/repo#12        Every outside source ent…
   ▸ 251.1   —          started    2026-11-20 01:12     0  slate-svc-runner                                  Compost the kitchen scraps
 ▸   expired · 1
 ▸   ended · newest 100  + reads 100 more
```

The panel on worklog 240.2:

```text
240.2 · preflight · planned · started 13:46
initiative 240 · Reminders fire on a date or on a checked condition

claim
claude-code/cda42040-8d12-4c30-9d5f-f1d9325ef5af
holds until 2026-11-25 13:46
waits on the user reading its work plan   14:02 → now

sessions
  cda42040   13:46 → now
  5e179cd4   13:50 → 14:01

links
  commit        docs  d50cd60  14:01

wrote  240/plan v1

tokens            uncached  cache write  cache read  output
this worklog           523       763.0k       58.5M  352.0k
its estimate             —            —           —    2.4M
240 all its work     12.9k        15.9M        1.2B    5.3M
240 estimate             —            —           —    2.4M
240's estimate is the one preflight 240.2 set
```

A worklog is one go at one record, and the tab answers what is under way now, what lost its
claim, and what just ended. Each row names the worklog's step, `—` where it names none, its stage,
its start, its sessions, its holder and what its claim waits on. A holder reads as its session's
first group, as a person tells sessions apart, or as the holder's name where no session holds it.

**Every read names its state and a limit.** Every worklog of every state answers megabytes, and
`tab-prior-art-and-read-costs.md` measures each read. The live and the expired read every 15
seconds, each up to `worklogsReadMost`. The ended read asks for the newest
`endedWorklogsStep` once a minute, and `+` and `-` move it by `endedWorklogsStep` up to
`worklogsReadMost`. A group whose read reached its
limit says `newest` and the limit, so a cut list never reads as the whole. A worklog that
changed state between two reads sits once, under the read that landed last, so no key draws
twice.

- *Rejected:* bounding `GET /worklogs`'s default page. That changes every client of the route,
  so the tab names its limit on every read instead.

**The panel is the worklog whole.** It holds the claim and each wait of its holder, the open
one in yellow, each session's span, and each link with its repo and when it was linked. The `wrote` line names each document version the worklog wrote. Then a table of tokens
by kind: the worklog's own, its own estimate where it holds one, and its record's total and
estimate. Only a preflight worklog holds an estimate. A worklog with no reported tokens says
`none reported`.

**The record's figures come from `GET /totals?record=`**, the read `slate totals` prints. It
answers the record's tokens over all its work and the estimate the latest preflight set, and
names that worklog. It reads once the cursor lands on a worklog, and again each minute while the
cursor stays. Each record's read is kept, so moving back draws at once. A failed read says so in
the panel.

`enter` unfolds a worklog into a row per session, then a row per link, as an issue unfolds on the
initiatives tab. `enter` on a pull request hands it to `prs tui --pr`. `o` opens an `http` or
`https` link in the browser and names any other, since a commit's ref is a hash and a worklog's
link is free text.

The tab declares the `worklog` kind. A worklog's row names the worklog, its record and each pull
request it links. So `t` there lands on the record's tab, and asks first where the worklog links a
pull request. A worklog past the ended read's bound is unread, so a jump to it reads
`worklogs holds no worklog 251.1`. `--json` prints what
`cli/internal/cli/tui-worklogs.schema.json` describes: each group's worklogs under the names that
`slate worklogs list --json` gives them, then the cursor's record's totals under the names that
`slate totals --json` gives them.

## `v` groups the same initiatives by tier, repo, pursuit, tag or alone

Tier is the default, because priority is the order in which the queue takes them. A pin outranks
every priority, so pinned initiatives draw as a `pinned` tier ahead of `urgent`, and each
priority's tier counts only its unpinned ones. The board read rolls the pinned up apart, so
`pinned · 1` sums no count in the client. Repo draws a group
per repo the issues name, the most open first and `no repo` last. Under it sits each initiative
with an issue there, cut to its issues there, so its bar and counts are that repo's share. An
initiative spanning three repos draws three times, and one with no issues sits under `no repo`.
Initiative drops the tier headings and leaves the list order. The scope still decides which
initiatives show. `--grouping` opens the tab at any of them, so `--once` draws what `v` would
show.

The board read carries each repo's rollup and each initiative's cut by repo, counted by the same
code as its tiers. So the repo grouping needs no second read, and the client derives no count.
A CLI released ahead of its API says the API answers no counts by repo where the groups would be.

**An initiative drawn more than once carries its group in every key under it.** The cursor
finds its row by key, so two rows sharing one would pull the cursor back to the first on every
read. An initiative's key, its issues', and their sessions' and pull requests' all end in
`@` and the group.

## Pursuit and tag answer which way the work has been neglected

Pursuit draws a group per pursuit and tag one per tag, each holding every initiative in it
whole. An initiative in two pursuits draws under both, and one in none under `no pursuit`,
last. The groups come with the fewest closes in 30 days first, so the pursuit in which nothing has
moved leads the screen. That is Slurm's fair-share without assigned shares: the least served goes
first. An active pursuit holding no initiative still draws, at zero, and so does an unused tag.
A group whose initiatives all sit past the scope draws too, as `Neglected Way · 0 of 1`. A
pursuit whose work all ranks low is the one that this grouping exists to show. The board read
names each group's members, so the client never decides which initiatives a group holds.

A group's line is its rollup on the same columns, and the line under it names its active
initiative touched longest ago and how long ago. The group's closes, 24H, 7D, 30D, LAST and the
spark, count every initiative in it, including one completed inside the window that the board
does not hold. DONE, RUN, BLK and RDY count only the initiatives that the board holds. So a pursuit
whose work finished last week still shows the effort. The panel says so. `docs/issues.md` says
what counts as a touch.

- *Rejected:* summing the drawn initiatives' closes in the client. The board holds only the
  initiatives at one status, so a pursuit's completed work would vanish from it the day the
  initiative closed.

## An issue unfolds into the sessions and pull requests that its worklogs hold

An issue is a folding group rather than a row, so the cursor can land on what it opens.
A pull request row is owned by prs, so `enter` there hands the terminal to
`prs tui --pr <url>`, and quitting prs comes back to the board, which reads again. After prs
exits non-zero, the board shows the last line prs wrote to stderr, or its exit status where it
wrote nothing. A usage error, exit 2, means the `prs` on `PATH` takes no `tui --pr`, and the
footer says so. `o` opens the pull request in the browser from any of its rows.

A worklog's link is free text any writer sets, and a desktop opener honors `file:`, `ssh:` and
every scheme an app registers. So `enter` and `o` hand on only an `http` or `https` link, and
name any other.

A session row opens to its directory, its moments and its tokens by kind. The worklogs are
read with the cursor's row, so a folded issue names its counts once the cursor has been on it.

- *Rejected:* the pull requests as lines of the issue's panel. A panel line is not a row, so
  nothing could open one.

## Each kind lands on one tab, so `t` goes to the tab that draws the record whole

Every line names what it is about, as gotermboard's entities. `t` goes from a line to the tab
declaring that kind, and `esc` comes back to the line it left. `*` locks every tab to the lines
naming one thing. gotermboard asks which tab where two declare a kind, so each kind is declared on
one tab, once, in `cli/internal/cli/tui_entities.go`. The record kinds are spelled as slate spells
them and named by number, since they share one sequence. A worklog is named by its name, and a pull
request by its link.

| Kind | Lands on | A line naming it |
| --- | --- | --- |
| initiative | initiatives | its row on the initiatives tab |
| issue | initiatives, under its initiative | its row on the initiatives tab |
| idea | ideas | its row on the ideas tab |
| pursuit | pursuits, at the focus in force | each initiative and idea in it, its group under `v` pursuit, and its row on the pursuits tab |
| worklog | worklogs | its row and each row under it on the worklogs tab, and each pull request and session row it holds on the initiatives tab |
| pull request | `prs tui --pr`, outside | its row under an issue, and a worklog's row and link row on the worklogs tab |

A tab that declares a kind in its `EntityKinds` adds its row here. `t` never offers the tab the
row is on, so a jump to an initiative, an issue, an idea or a pursuit comes from a row on another tab. The
documents tab declares no kind, and each of its rows names its record, of whatever kind.

**A jump to an initiative draws it, whatever the scope.** The per-tier scope draws the first few of
each tier, and the rest sit in a group that draws only under a filter, where gotermboard's seek
finds no line. So the sought initiative, or the one holding the sought issue, counts as shown until
the reader leaves the tab.

**The status widens only where a read lacks the record.** `GET /initiatives/board` answers about
0.5 MB at active and 1.2 MB at every status, each in under 1 s, on a board of 92 active initiatives
and 225 in all. `curl -w '%{size_download} %{time_total}'` with a bearer token measures them again.
A board reading every status on every poll pays that for nothing. Where the last read lacks the
record, the jump widens to every status at once. A tab that has not read
yet learns that from its first read, which reads every status after it only where its answer
lacks the record. The status narrows back once the reader leaves for good. A chained jump that `esc`
can still return along keeps it wide, so `esc` finds the row it left.

**`*` narrows by what each line names, and nothing more.** A lock on a pursuit keeps the
initiatives and the ideas in it, past the scope included, and each issue under a kept initiative.

**`t` and `enter` on a pull request take one door to `prs tui --pr`.** `enter` returns
gotermboard's `GoTo` to the program outside named prs, so the checks before prs opens and the
footer after it ends are written once, in `prsOnPullRequest`. prs opens only on a web link with
`prs` on `PATH`. Otherwise the footer says which is missing and nothing opens. A pull request's
row under an issue also names its worklog, so `t` there asks which: prs, or the worklog's row on
the worklogs tab. `enter` still goes straight to prs.

- *Rejected:* reading every status whenever the tab had not read. A jump from another tab to an
  active initiative would then draw the completed ones too.

## Keys that write run what the slate verb runs, one at a time

`M` moves an issue to the initiative typed, as `slate issues edit --initiative` does. `P` pins
the row, or unpins it where it is pinned, as `pin` and `unpin` do. `<` and `>` move the row
ahead of the row drawn above it or behind the row drawn below, as `reorder --before` and
`--after` do. An answer's warnings follow what the footer says it did, and a refusal shows in
the API's own words.

One write runs at a time, with the footer naming it, and the board reads everything again when
it ends, refused or not. A write that timed out or lost its connection may have landed, so the
footer says its outcome is unknown rather than that it failed. An initiative moves only within
its priority and only while sorted by priority, because otherwise the rows beside it are not its
neighbors in the queue.

A write acts at once on the row under the cursor. It asks first where the screen does not show
everything it changes:

- the row that the move passes is one the scope hides;
- the queue holds records between the two rows that are not drawn there, as under a repo, or
  beside a running issue drawn at the top of its initiative, and the question names them;
- another row's description is left open by `d`.

A reader acts on what the screen shows, so a key that writes past what the screen draws asks
first.

## `C` opens a Claude session beside the board

Inside tmux, `C` opens `claude` in a new window, so the board stays usable while a long
session works. tmux runs a window's command and its arguments without a shell, so the first
message needs no quoting. Outside tmux, `C` hands the board's terminal to `claude` and takes it
back when the session ends.

A session about an issue belongs in its repo's code, so `C` opens in the checkout the repo
registry's `path` names for the issue's repo, or for an initiative's where its issues name one.
A checkout named and missing opens nothing. Where the registry names none, or the record no one
repo, the session opens in the board's directory, and the footer names that directory and why.

The first message names the record by its number alone and asks the session to read it with
`slate show`, then say what it would do before changing anything. A title is free text another
writer set, so it reaches the session as data it reads rather than as its first instruction.

## A description is rendered markdown, on a renderer of our own

Descriptions are written as markdown, with headings, lists, code, tables and diagrams, and a
person opens one to read it. So `d`, `enter` and the panel all draw it through gotermboard's
`markdown` package: colored headings, bullets whose wrapped lines hang under their text, tables
that keep a short column whole, boxes of code highlighted in the terminal's own colors. A number
in prose draws in the color that the board gives records, where it names a record on the board.

- *Rejected:* glamour. Its wrapped bullet lines fall back to the margin, lists are in most
  descriptions, and it answers one styled string that a board cannot color or wrap further.
- *Rejected:* `glow` as a subprocess. It needs a binary on the path, and its margins and heading
  bands fight a board's own layout.
- *Rejected:* a description's first paragraph as its summary. `docs/issues.md` says why it is a field.
