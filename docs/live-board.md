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

## Each initiative is two lines: its name, then dim repos beside its counts

```text
                                    DONE  24H   7D  30D  LAST  RUN  BLK  RDY  ISSUES
   TOTAL · 85                   588/1175   27  125  372   16m   23  207  281  ▰▰▰▰▰▰▰▰▰▰▱✕✕✕▱▱▱▱▱▱ 50%  ▅ ▂▁

 ▾ high · 5 of 50                453/786   24   87  294   16m   22  135  128  ▰▰▰▰▰▰▰▰▰▰▰▰▱✕✕✕▱▱▱▱  ▆ ▂▂
›  ▸ 1544  An agent keeps the queue ordered toward its goals as proposals a person steers
           api, cli, docs, +6      19/48    9   11   19    3h   14    8    7  ▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰▱▱▱▱▱▱▱▱▱▱▱▱▱▱✕✕✕✕✕✕✕✕▱▱▱▱▱▱▱
```

A number and a name are what a person scans for, so they lead, the name in bold, and a name wraps
under itself rather than being cut. Columns let the eye run down one figure across every initiative,
and a tier's totals line up over the rows they sum. Repos are read when acting on a row rather
than when scanning, so they stay dim in the first column and end in `+N`, because a repo cut in
half names nothing. The side panel lists them all, and the filter matches every one.

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
and a rollout never blanks the screen.

**What only one row shows is read for that row alone.** An initiative's next issues and its
tokens by kind come from `GET /initiatives/progress`, and an issue's whole description from
`GET /issues/{id}`. Reading those for every row is what the board read exists to avoid. So
the read follows the cursor, on the next tick after it lands, which means holding a key down
reads only the row it stops on. A row read once draws at once when the cursor returns.

## Opening a row uses separate keys

`enter` unfolds an initiative's issues and opens an issue's description. `d` opens an
initiative's description, and `f` adds the initiative's summary and each issue's first
paragraph. Each answers a different question, so one key that cycled through them would make the
reader step past two answers to reach the third. Unfolded issues go open first, running at the
top and then in queue order, with `done` and `canceled` folded beneath.

A description that fits opens under its row. One that runs past the screen opens on a full-screen
reader, where `[` and `]` jump between its headings, `/` searches it, and `esc` returns to the row.
`space` opens a description under its row however long it is, and closes it again.

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
any of its ready issues has waited, and a spark of its closes in each of the weeks the API
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

The board is read-only. Pinning and reordering stay in the CLI, where a write says everything
it did.

## A description is rendered markdown, on a renderer of our own

Descriptions are written as markdown, with headings, lists, code, tables and diagrams, and a
person opens one to read it. So `d`, `enter` and the panel all draw it through gotermboard's
`markdown` package: colored headings, bullets whose wrapped lines hang under their text, tables
that keep a short column whole, boxes of code highlighted in the terminal's own colors. A number
in prose draws in the color the board gives records, where it names a record on the board.

- *Rejected:* glamour. Its wrapped bullet lines fall back to the margin, lists are in most
  descriptions, and it answers one styled string that a board cannot color or wrap further.
- *Rejected:* `glow` as a subprocess. It needs a binary on the path, and its margins and heading
  bands fight a board's own layout.
- *Rejected:* a description's first paragraph as its summary. `docs/issues.md` says why it is a field.
