# Focus

A focus states where the work should go for a while: a statement in its
setter's words, the last day it holds, and the share of the work that each
pursuit should get. `GET /api/v1/focus` reads it with each pursuit's standing
against its share, which slate computes on every read. A reader that orders the
queue takes slate's numbers rather than working them out again.

The pattern is fair-share scheduling, as Slurm's classic fair-share computes a
factor from an account's assigned share and its recent usage. What a share
means is engineering investment allocation, the split of effort across goals
that tools such as Jellyfish and LinearB report.

## One focus is in force at a time, and every focus stays

| Route | Does | Scope |
| --- | --- | --- |
| `GET /focus` | the focus in force and each pursuit's standing | `slate.records.read` |
| `GET /focus/history` | every focus, the latest set first | `slate.records.read` |
| `PUT /focus` | sets a focus from now | `slate.focus.write` |
| `POST /focus/end` | ends the focus in force, with a reason | `slate.focus.write` |

`slate focus show`, `list`, `set` and `end` drive the four routes in that
order. `set` takes each share as `--share <pursuit>=<fraction>`, the pursuit
by number or exact name, and reads a name to its number before the PUT.

`focuses` holds each focus: `statement`, `started_ts`, `until_date`,
`ended_ts`, `ended_reason` and `set_by`. `focus_shares` holds one row per
pursuit a focus names, with its `share`, a fraction from 0 to 1.

- **A partial unique index lets one row at a time leave `ended_ts` null.**
  Setting a focus ends that row as `replaced`, with the side effect
  `focus_replaced`. So the history is a read of every row.
- **A focus past its `until_date` reads as ended, though nothing wrote its
  end.** `until_date` is its last day, in the request's `timezone`, else UTC.
  No job sweeps it. The next set ends that row as `lapsed`, with the side
  effect `lapsed_focus_ended`. An end writes its reason, with a `focus_lapsed`
  warning.
- **`ended_reason` tells a set's end from a person's.** A set writes
  `replaced` or `lapsed`, and an end refuses either as its reason.
- **A set naming what the focus in force holds changes nothing.** Its
  statement, `until_date` and shares match, so it answers that focus with a
  `focus_unchanged` warning. A caller retrying its PUT neither ends the focus
  it set nor restarts the usage window.
- **Two sets at once both succeed, the later replacing the earlier.** Each set
  and end takes one transaction-scoped advisory lock before it reads the
  unended row. Without it, the later set would meet the unique index and fail.
- **An end with no focus in force changes nothing.** It answers 200 with a
  `no_focus_to_end` warning, as a release with no claim does.
- **`set_by` is the caller, signed as its other writes are.** A person signs
  as their username, else their `sub`, and a service as its client id. The
  audit trail names who ended one.

A set refuses with 422:

- a pursuit named twice,
- a pursuit that is not active,
- shares summing past 1,
- an `until_date` before today.

A set whose shares name every active pursuit and sum under 1 goes through with
a `shares_under_one` warning, because the rest of the work is then no
pursuit's share. Both checks read a sum within `SHARE_SUM_TOLERANCE` of 1 as
1, because shares a client divided in floating point, such as three of 1/3,
miss 1 by about 1e-16.

## Shares fill to 1 on read

A share the focus named for an active pursuit is taken as given. The active
pursuits it left unnamed split the rest of 1 evenly. A named pursuit that is
no longer active leaves, and its part goes to the unnamed ones.
`share_named` says which a pursuit's share is.

With no focus in force there are no shares. `share` and `factor` read null,
and usage still reads.

*Rejected:* equal shares when no focus is set. Every pursuit would weigh the
same, however little it matters this month.

## Usage is output tokens, split evenly across the pursuits that a worklog reaches

The window opens at the later of the focus's start and `USAGE_WINDOW` in
`services/focus_standing.py` before the read, and ends at the read. `window`
gives both instants.

- **Each worklog's output in the window splits evenly across the pursuits its
  record reaches.** An idea or an initiative reaches its own pursuits. An issue
  reaches its initiative's, as an issue's read shows them. A worklog on an
  initiative in two pursuits gives each half its output.
- **`usage` is a pursuit's part of the output that every pursuit drew.** The
  pursuits' usages sum to 1 where any drew some. `split_output_tokens` is the
  split count it comes from, so it may hold a fraction. Every other token count
  slate answers is a whole number, so this one carries its own name.
- **`unattributed` is the output that reached no pursuit.** `output_tokens` and
  `worklog_count` are the worklogs whose record reaches none.
  `outside_worklogs_output_tokens` is what sessions spent in no worklog.

The focus splits each worklog across the pursuits that its own record joins, or
that its issue's initiative joins, as `services/record_pursuits.py` reads them
for every record. `slate totals <pursuit>` counts a pursuit as a grouping over
everything its work reaches, as [totals.md](totals.md) gives it. So a total's
figure for a pursuit can exceed the focus's.

*Rejected:* worklog count as usage. A one-line fix would count as much as a
week-long build. *Rejected:* a decay half-life. A window opening at the
focus's start does the same job for a focus lasting weeks, with one parameter
fewer.

## The factor is 2^(-usage/share)

`factor` reads 0.5 at the share, toward 1 under it and toward 0 over it. A
share of 0 reads 0, because any work at all is over it. `pursuits` lists the
furthest under its share first: the highest factor, then the least output,
then the number.

```text
usage  share   factor
0.0    0.25    1.0     under: no work yet
0.2    0.25    0.57
0.25   0.25    0.5     at its share
0.8    0.5     0.33    over
```

`touched_ts` is the latest `touched_ts` of any initiative in the pursuit, as
[issues.md](issues.md) defines it for the board and `last_touched` in
`services/initiative_board.py` reads it for both. It is null where the pursuit
holds no initiative.

*Rejected:* slate sorting the queue by the factor on every read. The order
would change with no write, so a diff of the order would stop describing it,
and rank would stop being the stored order. A reader applies the factor as a
write. *Rejected:* a share column on the pursuit. A focus starts, changes and
ends, and a column keeps neither the statement nor the history.

## Pursuits listed are the active ones and any other that drew output

Every active pursuit has a row. A pursuit that is not active has a row only
where it drew output in the window, with its `status`, and with `share` and
`factor` null. Its output still counts in every other pursuit's
`usage`, because that work happened.

`services/focus_standing.py` computes the read, and `services/focuses.py`
holds the writes.
