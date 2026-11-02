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
and the worklog span the session was in then. The row's key is the message
alone, with no surrogate, because the row mirrors a message the transcript
already names.

Claude Code can continue a session under a new id. The new transcript repeats
the parent's messages, with their ids. So the session that reports a message
first holds it, and a later report naming it warns
`messages_held_by_another_session`. Each message then counts once.

A message is the grain any window can split at. A seven-day window, or one
day in any time zone, may cut through a span. A message has one moment, so it
falls on one side of every edge. Any sum, over any grouping or window, reads
these same rows. `slate totals` sums them over any record, worklog, repo or
window ([totals.md](totals.md)).

- *Rejected:* one row per span with its sums. A window whose edge cuts the
  span would need the sum split by time, which the row cannot give.
- *Rejected:* the audit trail on these rows. A report replaces a session's
  rows wholesale, and the transcript is already their record. Each report that
  changes them sets `agent_sessions.usage_reported_ts`, and that row's audit
  names who reported. A report that changes nothing is not audited.

## Each message goes to the span that held its moment

A span holds the moments from its `started_ts` up to its `ended_ts`. A span
still open in a worklog whose claim ended or ran out stops where the claim
did, and every read gives that moment as its `ended_ts`. A message no span
holds is the session's alone, so a window's total
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
none. Either way it is unique across sessions. Each message the report names
is upserted on its key. A row is written only when something the report
carries for it differs from what slate holds. Each row the report leaves out
is deleted. So a session resumed under its own id reports its whole transcript
again and counts nothing twice. A report naming exactly what slate holds
writes nothing and warns `usage_unchanged`.

A report replaces only one it read at least as far as. A transcript only
grows, so a report that read less is an earlier read, or one that came back
empty or cut. Its newest message from the session's own transcript must be no
older than the newest slate holds from it, and the same holds for each
subagent's transcript slate holds messages from. A report that falls short in
any of them changes nothing, and warns `usage_stale` naming each one. A
message's output never shrinks either, so a read that caught a message
mid-write keeps the figure slate holds.

The session's row is locked while a report replaces its messages. Two reports
of one session, such as a turn's and the session's last, then apply one after
the other, and whichever read less changes nothing.

The write takes counts, never a transcript, so any agent reports its own the
same way. `GET /sessions/{id}` reads the session back: its message count, and
its tokens in all, in each span, and outside every worklog. A worklog's read
sums its spans', and `slate worklogs show` prints that line. A sum over no
reported message is null, never zero, so a span nobody reported for never
reads as having spent nothing. The part outside every worklog is a count once
the session has reported any message, and zero when every one fell in a
worklog. A session that works only inside worklogs is common, and null there
would say it never reported.

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

`usage.iterations` lists each model call behind a message. The report reads
the top-level `usage`, never the iterations. In Claude Code 2.1.316's
transcripts, each message held one iteration equal to the top level.

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

- **SessionEnd gives the final count.** Claude Code 2.1.316 ran it when the
  session exited, when its terminal closed, and when its tmux pane was killed,
  idle or mid-tool. A hook still running after the hangup finished. Its
  default hook budget was 1.5 seconds, too short for a token refresh and the
  request, so `timeout: 30` raises it.
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
authenticates as a service client. Register that client for the scopes that
`slate sessions report --help` and `slate sessions show --help` name under
`Service scopes:`. A report names no actor, so where it changes the session,
the audit row names the client.
