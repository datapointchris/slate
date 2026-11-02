# Sessions

An agent session is one run of a coding agent, keyed by the id its runtime
gave it: `$CLAUDE_CODE_SESSION_ID` for Claude Code. slate records a session the
first time it holds a claim, joins a worklog, links a commit or reports its
tokens. `slate sessions` is the CLI, and the API serves it under
`/api/v1/sessions`.

## Tokens are counted by kind, never summed across kinds and never priced

Each count names one of four kinds:

| Kind | Claude's usage field | What it is |
| --- | --- | --- |
| `uncached_input` | `input_tokens` | input read without the prompt cache |
| `cache_write` | `cache_creation_input_tokens` | input written to the cache |
| `cache_read` | `cache_read_input_tokens` | input read from the cache |
| `output` | `output_tokens` | what the model wrote |

The kinds cost different amounts, and the cost per kind changes by model and
over time. So a read carries the four counts, and anything that prices them
does so outside slate.

## A session's tokens are stored per message

`message_tokens` holds one row per model message a session reported: its four
counts, when it was written, its model, the subagent whose transcript held it,
and the worklog span the session was in then. The row's key is the session and
the message, with no surrogate, because the row mirrors a message the
transcript already names.

A message is the grain any window can split at. A seven-day window, or one
day in any time zone, may cut through a span. A message has one moment, so it
falls on one side of every edge. Sums by worklog, record, initiative, model or
window all read the same rows.

- *Rejected:* one row per span with its sums. A window whose edge cuts the
  span would need the sum split by time, which the row cannot give.
- *Rejected:* the audit trail on these rows. A report replaces a session's
  rows wholesale, and the transcript is already their record. Each report sets
  `agent_sessions.usage_reported_ts`, and that row's audit names who reported.

## Each message goes to the span that held its moment

A span holds the moments from its `started_ts` up to its `ended_ts`. A span
still open in a worklog whose claim ended or ran out stops where the claim
did. A message no span holds is the session's alone, so a window's total
includes time outside every worklog. A session is in one worklog at a time
([worklogs.md](worklogs.md)), so each message lands in one place.

The API places each message when the report arrives. It compares the
message's `written_ts`, on the agent's clock, with the span's times, on the
API's.

Deleting a record deletes its worklogs and their spans. Their messages stay,
as the session's alone, so a window still counts the tokens they spent.

## A report replaces the session's last one

`PUT /sessions/{id}/usage` takes every message the session has spent tokens on
so far:

```json
{
  "host": "desk",
  "messages": [
    {
      "key": "msg_011Cge8ZoRhMyNdctMJv3o1S",
      "written_ts": "2026-11-02T12:51:05.698Z",
      "model": "claude-opus-5-5",
      "subagent": null,
      "tokens": {"uncached_input": 2, "cache_write": 32623, "cache_read": 20679, "output": 141}
    }
  ]
}
```

`key` is the provider's message id, or where the message was read when it has
none. Each message the report names is upserted on its key. A row is written
only when its counts, model or span changed. Each row the report leaves out is
deleted. So a resumed session reports its whole transcript again and counts
nothing twice. A report naming exactly what slate holds writes nothing and
warns `usage_unchanged`.

The session's row is locked while a report replaces its messages. Two reports
of one session, such as a turn's and the session's last, then apply one after
the other.

The write takes counts, never a transcript, so any agent reports its own the
same way. `GET /sessions/{id}` reads the session back: its tokens in all, in
each span, and outside every worklog. A worklog's read sums its spans', and
`slate worklogs show` prints that line.

## `slate sessions report` counts each message in a Claude Code transcript once

Claude Code writes a session to `<project>/<session>.jsonl`. It writes one
line per content block of a message, and each line repeats the whole message's
`usage`. So the report keys each message by `message.id`, and takes the line
with the most output as its final figure. A message with no id is keyed by its
file and line. A line that does not parse is passed over, because a running
session's last line can be cut short.

A subagent's tokens are the session's spend. Claude Code writes each subagent
to `agent-<id>.jsonl` under `<project>/<session>/subagents/`. An older Claude
Code wrote it flat beside the session as `<project>/agent-<id>.jsonl`, which
names no session. So a flat one counts only where the session's own
transcript names the subagent's id in a tool result's `agentId`.

`usage.iterations` lists each model call behind a message. Claude Code writes
one iteration equal to the top-level `usage`, so the report reads the top
level.

## A Claude Code hook reports at every turn and at the session's end

Add this to `~/.claude/settings.json` to report every session on the machine,
or to a repository's `.claude/settings.json` for that repository alone:

```json
{
  "hooks": {
    "SessionEnd": [
      {"hooks": [{"type": "command", "command": "slate sessions report >/dev/null", "timeout": 30}]}
    ],
    "Stop": [
      {"hooks": [{"type": "command", "command": "slate sessions report >/dev/null", "async": true}]}
    ]
  }
}
```

Claude Code hands a hook its event as JSON on stdin, and the report reads
`session_id` and `transcript_path` from it.

- **SessionEnd gives the final count.** Claude Code runs it when the session
  exits and when its terminal closes. A killed tmux pane runs it too, whether
  the session was idle or running a tool. A hook still running after the
  hangup finishes. The hooks share a 1.5-second budget by default, and
  `timeout: 30` raises it so a token refresh and the request fit.
- **Stop keeps a long session's count current.** Nothing runs when the agent
  is killed with SIGKILL or its machine goes down. So a report after each turn
  keeps what slate holds no more than a turn behind. `async` keeps the turn
  from waiting on it. The transcript may not hold a turn's last message when
  Stop fires, so that message arrives with the next report.
- **`>/dev/null` drops the summary line**, which a hook has no reader for.
  Warnings and errors go to stderr.

Run by hand, `slate sessions report --transcript <path>` reports the session
the file is named for.

In a container, the agent's settings carry the same block. The agent
authenticates as a service client holding `slate.sessions.write`, which
reaches the report and nothing else. `slate.records.read` reaches a session's
read. A report names no actor, so its audit row names the client.
