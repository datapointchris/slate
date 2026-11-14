# Focus

A focus states where the work should go for a while: a statement in its
setter's words, the last day it holds, and the share of the work each pursuit
should get. `GET /api/v1/focus` reads it with each pursuit's standing against
its share, which slate computes on every read. A reader that orders the queue
takes slate's numbers rather than working them out again.

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

`focuses` holds each focus: `statement`, `started_ts`, `until_date`,
`ended_ts`, `ended_reason` and `set_by`. `focus_shares` holds one row per
pursuit a focus names, with its `share`, a fraction from 0 to 1.

- **A partial unique index lets one row at a time leave `ended_ts` null.**
  Setting a focus ends that row as `replaced`, so the history is a read of
  every row.
- **A focus past its `until_date` reads as ended, though nothing wrote its
  end.** `until_date` is its last day, in the request's `timezone`, else UTC.
  No job sweeps it. The next set ends that row as `lapsed`, and an end writes
  its reason with a `focus_lapsed` warning.
- **Two sets at once both succeed, the later replacing the earlier.** Each set
  and end takes one transaction-scoped advisory lock before it reads the
  unended row. Without it, the later set would meet the unique index and fail.
- **An end with no focus in force changes nothing.** It answers 200 with a
  `no_focus_to_end` warning, as a release with no claim does.
- **`set_by` is the caller.** A person signs as their username, a service as
  its client id. The audit trail names who ended one.

A set refuses with 422:

- a pursuit named twice,
- a pursuit that is not active,
- shares summing past 1,
- an `until_date` before today.

A set whose shares name every active pursuit and sum under 1 goes through with
a `shares_under_one` warning, because the rest of the work is then no
pursuit's share.

## Shares fill to 1 on read

A share the focus named for an active pursuit is taken as given. The active
pursuits it left unnamed split the rest of 1 evenly. A named pursuit that has
since completed or dropped leaves, and its part goes to the unnamed ones.
`share_named` says which a pursuit's share is.

With no focus in force there are no shares. `share` and `factor` read null,
and usage still reads.

*Rejected:* equal shares when no focus is set. Every pursuit would weigh the
same, however little it matters this month.

## Usage is output tokens, split evenly across the pursuits a worklog reaches

The window opens at the later of the focus's start and 30 days before the
read, and ends at the read. `window` gives both instants.

- **Each worklog's output in the window splits evenly across the pursuits its
  record reaches.** An idea or an initiative reaches its own pursuits. An issue
  reaches its initiative's, as an issue's read shows them. A worklog on an
  initiative in two pursuits gives each half its output.
- **`usage` is a pursuit's part of the output every pursuit drew.** The
  pursuits' usages sum to 1 where any drew some. `output_tokens` is the split
  count it comes from, so it may hold a fraction.
- **`unattributed` is the output that reached no pursuit.** `output_tokens` and
  `worklog_count` are the worklogs whose record reaches none.
  `outside_worklogs_output_tokens` is what sessions spent in no worklog.

This differs from `slate totals <pursuit>` in two ways.
[totals.md](totals.md) counts a pursuit as a grouping, so a worklog in two
pursuits counts whole in each. A total also walks a child issue to its
parent's initiative, where the focus reads only the issue's own initiative.

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

`last_worked_ts` is the latest touch on any initiative in the pursuit, as
`touched_at` in `services/initiative_board.py` reads one for the live board:
its creation, a claim starting on it or an issue, or an issue closing. It is
null where the pursuit holds no initiative.

*Rejected:* slate sorting the queue by the factor on every read. The order
would change with no write, so a diff of the order would stop describing it,
and rank would stop being the stored order. A reader applies the factor as a
write. *Rejected:* a share column on the pursuit. A focus starts, changes and
ends, and a column keeps neither the statement nor the history.

## Pursuits listed are the active ones and any other that drew output

Every active pursuit has a row. A completed, dropped or shelved pursuit has
one only where it drew output in the window, with its `status`, and with
`share` and `factor` null. Its output still counts in every other pursuit's
`usage`, because that work happened.

`services/focus_standing.py` computes the read, and `services/focuses.py`
holds the writes.
