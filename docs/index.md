# slate docs

| Page | What it holds |
| --- | --- |
| [records.md](records.md) | Ideas, initiatives and issues as one numbered record: the `records` table, why each kind references it, the read of any record by number, its changes read back from the audit trail, the envelope every write answers, and the duplicate warning a create gives with the measurement behind its bar |
| [ideas.md](ideas.md) | Ideas as work nobody has taken on: their own priority, evidence linked by key or URL, and promotion into an initiative |
| [issues.md](issues.md) | The model: what makes an issue ready, the steps `next` lists every kind at, what a dependency means, how the queue keeps each initiative's work together, the paths an issue changes, how closing works, the one state an initiative's progress counts each issue in, and what is deliberately not built |
| [claims.md](claims.md) | Claims as leases on any record, kept after they end: the one refusal, the warnings a hold gives, the waiting state, and how a claim expires and ends |
| [worklogs.md](worklogs.md) | Worklogs as each go at a record: their names, what opens and ends one, why a session is in one at a time, and how a commit reaches its worklog |
| [sessions.md](sessions.md) | Each agent session's tokens by kind: why they are stored per message, how a message finds its worklog span, why a report replaces the last, and the Claude Code hook that reports them |
| [documents-and-comments.md](documents-and-comments.md) | Research, design and plan documents on any record, versioned, and comments on a record or a line: who wrote each, from which worklog, and how a line comment follows its line into a later version |
| [tracing.md](tracing.md) | OpenTelemetry in the API and the CLI: the variables that configure it, why it exports nothing unless told, the record each span names and why only one, the events naming every other record a write changed, and the trace each log line and audit row carries |
| [totals.md](totals.md) | The work's totals over any record, worklog, repo or window: what each scope reaches, why a repo's total is its issues' worklogs, and what a window counts of each figure |
