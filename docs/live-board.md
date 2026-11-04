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

## Each initiative is three lines on columns the totals share

```text
                        DONE  24H   7D  30D  LAST  RUN  BLK  RDY  ISSUES
   TOTAL · 85       588/1175   27  125  372   16m   23  207  281  ▰▰▰▰▰▱▱▱▱▱ 50%  ▅ ▂▁  ▇█▄▃▂▅

 ▾ high · 5 of 50    453/786   24   87  294   16m   22  135  128  ▰▰▰▰▱▱▱▱▱▱  ▆ ▂▂  ▇█▆▅▄█
›  ▸ 1544              19/48    9   11   19    3h   14    8    7  ▰▰▰▰▰▰▰▰▱▱▱▱▱
     An agent keeps the queue ordered toward its goals as proposals a person steers
     api, cli, docs, web, worker, +4
```

Columns let the eye run down one figure across every initiative, and a tier's totals line
up over the rows they sum. A name and its repos are too long for a column, so they take
lines of their own. Repos end in `+N`, because a repo cut in half at the screen's edge names
nothing. The width they have moves with the side panel, so the draw works out the `+N`.

- *Rejected:* a first line of words, `19 in 24h · 3 running`. It reads well once and cannot
  be compared down the screen.
- *Rejected:* name and repos on one line. Either one long value pushes the other off.

**The columns count what each read means, and nothing is derived twice.** DONE is completed
over completed and open, because `issue_count` includes the canceled. RUN adds running to
waiting, because the bar draws both yellow. 24H, 7D and 30D count completed issues only,
since the question is how much work finished. A zero reads `·`, so the numbers that are
there stand out.

## A bar has one cell per issue

`19/48` draws 48 cells: green done, yellow running or waiting, red blocked, plain the rest.
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
initiative's description in place, and `f` adds first lines under the initiative and each
issue. Each answers a different question, so one key that cycled through them would make the
reader step past two answers to reach the third. Unfolded issues go open first, running at
the top and then in queue order, with `done` and `canceled` folded beneath.

From 160 columns a side panel follows the cursor, with the description, the closes, what is
next and the tokens, so a wide terminal answers without a key. `p` hides it.

The board is read-only. Pinning and reordering stay in the CLI, where a write says everything
it did.
