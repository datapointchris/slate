# Prior art for the queue, pursuits, worklogs and documents tabs, and what their reads cost

`slate tui` gains a tab for the queue, the pursuits under the focus, the worklogs and the
documents. This page holds what other tools draw for each, and what slate's existing reads cost a
tab that polls them. [live-board.md](live-board.md) says why each tab has the shape it has.

## Other trackers give each kind of work its own section, and a scheduler splits a place in line into its factors

- **Linear** puts Inbox, My Issues, Triage, Projects, Initiatives and Documents in one sidebar.
  A document hangs off a project or an initiative, as slate's hangs off a record.
- **gh-dash** builds a terminal dashboard from sections, each a titled filter over one read, as a
  slate tab is a view over one read.
- **Slurm's `squeue`** lists jobs in the order they will run, and **`sprio`** splits each job's
  priority into its factors: age, fair-share, QOS and partition. The queue tab's panel does the
  same for a row: its tier, what it inherits from, its pin, its rank and its pursuit's focus factor.
- **Slurm's `sshare`** lists each account's assigned share, its usage and the fair-share factor
  that falls once usage passes the share. The focus computes Slurm's classic factor,
  `2^(-usage/share)`, so the pursuits tab is `sshare` over pursuits. [focus.md](focus.md) holds
  the formula.
- **Gerrit** ports an unresolved comment from the patch set it was written on to each later one, on
  the line its own became, and shows it there as the same comment rather than a copy. slate's
  document read places every line comment on the version read the same way.

## The queue and the ended worklogs are too heavy to poll, and focus and the documents are light

Each figure is one read against a queue of about 900 open records and about 1,650 ended worklogs.
`curl -w '%{size_download} %{time_total}'` with a bearer token measures them again.

| Read | Answers | Takes |
| --- | --- | --- |
| `GET /queue` | about 3 MB | about 4 s |
| `GET /worklogs?state=ended` | about 5 MB | about 3.5 s |
| `GET /initiatives/board` | about 0.5 MB | under 1 s |
| `GET /documents` | about 60 KB | under 1 s |
| `GET /focus` | about 5 KB | under 0.5 s |
| `GET /focus/usage` | about 4 KB a day for 16 pursuits, read at 2 days | under 0.5 s at 2 days |

- **A queue row carries its whole record.** Dropping every description still leaves about 1.5 MB,
  because each row holds the record's refs, claim, last check and steps. The tab draws a few
  hundred bytes of each row. [api-memory.md](api-memory.md) names `GET /queue` among the reads that grow
  the API's memory.
- **An ended worklog is about 3 KB**, with its spans, links and document versions. Reading the
  newest hundred costs about a tenth of reading them all.
- **`GET /focus` answers one total per pursuit over its whole window**, so a chart of usage by day
  needs a read that splits the same output by the day each message was written.
- **Most worklogs name no step, and some report no tokens.** Of the newest 200 ended, about three
  in five name no step and one in eight carries no tokens. A column for either reads empty rather
  than zero.
