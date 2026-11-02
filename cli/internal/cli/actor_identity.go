package cli

import "os"

// actorWorkerEnv names the variable a scheduler sets to say which of its
// workers is running, so what the worker does is signed with that name.
const actorWorkerEnv = "SLATE_ACTOR"

// actorIdentity names who acts when a claim, a comment or a close does not say.
// Two sessions on one machine must not share a name, because a hold under a
// name that already holds the record answers that it is held. The prefix is the kind of actor, so a reader can
// filter on it. docs/issues.md lists the kinds.
func actorIdentity() string {
	worker := os.Getenv(actorWorkerEnv)
	if session := os.Getenv("CLAUDE_CODE_SESSION_ID"); session != "" {
		switch {
		case worker != "":
			return "claude-code-unattended/" + worker + "/" + session
		case os.Getenv("CLAUDE_CODE_SESSION_ATTENDED") == "0":
			return "claude-code-unattended/" + session
		default:
			return "claude-code/" + session
		}
	}
	if worker != "" {
		return "worker/" + worker
	}
	user := os.Getenv("USER")
	if user == "" {
		user = "unknown"
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		return user
	}
	return user + "@" + host
}
