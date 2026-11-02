// Package transcript reads the tokens a Claude Code session spent from the
// transcripts it wrote: its own, and each of its subagents'.
//
// Claude Code writes a session to <project>/<session>.jsonl, and each subagent
// it starts to agent-<id>.jsonl, anywhere under <project>/<session>/subagents/.
// An older Claude Code wrote a subagent flat beside the session, as
// <project>/agent-<id>.jsonl, which names no session. So a flat one is read
// only where the session's own transcript names the subagent's id.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	transcriptSuffix = ".jsonl"
	subagentPrefix   = "agent-"
)

// Tokens is one message's tokens by kind.
type Tokens struct {
	UncachedInput int
	CacheWrite    int
	CacheRead     int
	Output        int
}

// Message is one model message that spent tokens.
type Message struct {
	// Key is the message's id, or where it was read when it carries none.
	Key       string
	WrittenAt time.Time
	Model     string
	// Subagent is the id of the subagent whose transcript holds it, and empty
	// for the session's own.
	Subagent string
	Tokens   Tokens
}

// SessionMessages is every model message the session whose own transcript is
// at path spent tokens on, counted once each, oldest first.
//
// Claude Code writes one line per content block of a message, each repeating
// the whole message's usage. So a message is counted once, by its id. Where
// its lines differ, the one reporting the most output is its final figure. A
// line that does not parse is passed over, because a running session's last
// line can be cut short.
func SessionMessages(path string) ([]Message, error) {
	project := filepath.Dir(path)
	messages := map[string]Message{}
	spawned, err := read(path, project, "", messages)
	if err != nil {
		return nil, err
	}
	subagents, err := subagentTranscripts(path, spawned)
	if err != nil {
		return nil, err
	}
	for _, id := range slices.Sorted(maps.Keys(subagents)) {
		if _, err := read(subagents[id], project, id, messages); err != nil {
			return nil, err
		}
	}
	out := make([]Message, 0, len(messages))
	for _, m := range messages {
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b Message) int {
		if c := a.WrittenAt.Compare(b.WrittenAt); c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
	return out, nil
}

// subagentTranscripts is each subagent transcript of the session at path, by
// the subagent's id: every one under the session's subagents directory, and a
// flat one beside the session for each id in spawned that has none there.
func subagentTranscripts(path string, spawned []string) (map[string]string, error) {
	project := filepath.Dir(path)
	session := strings.TrimSuffix(path, transcriptSuffix)
	found := map[string]string{}
	nested := filepath.Join(session, "subagents")
	if realDir(session) && realDir(nested) {
		// WalkDir follows no link, so a linked directory under it reaches nothing.
		err := filepath.WalkDir(nested, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if id, ok := subagentID(e.Name()); ok && e.Type().IsRegular() {
				found[id] = p
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("read the subagents of %s: %w", path, err)
		}
	}
	for _, id := range spawned {
		flat := filepath.Join(project, subagentPrefix+id+transcriptSuffix)
		if _, ok := found[id]; !ok && regularFile(flat) {
			found[id] = flat
		}
	}
	return found, nil
}

func subagentID(name string) (string, bool) {
	id, ok := strings.CutPrefix(name, subagentPrefix)
	if !ok {
		return "", false
	}
	return strings.CutSuffix(id, transcriptSuffix)
}

func realDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

func regularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

// line is what a transcript line carries that this package reads.
type line struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Input         int `json:"input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
			Output        int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

var (
	usageField   = []byte(`"usage"`)
	agentIDField = []byte(`"agentId"`)
)

// read adds each message the transcript at path holds to messages, and
// returns each subagent it started, in the order it names them. A message
// with no id is keyed by its file, relative to project, and line number.
func read(path, project, subagent string, messages map[string]Message) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	where, err := filepath.Rel(project, path)
	if err != nil {
		where = path
	}
	var spawned []string
	r := bufio.NewReader(f)
	for n := 1; ; n++ {
		raw, err := r.ReadBytes('\n')
		if bytes.Contains(raw, usageField) || bytes.Contains(raw, agentIDField) {
			var l line
			if json.Unmarshal(raw, &l) == nil {
				add(l, where+":"+strconv.Itoa(n), subagent, messages)
				if id := spawnedAgent(l.ToolUseResult); id != "" && !slices.Contains(spawned, id) {
					spawned = append(spawned, id)
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return spawned, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
	}
}

// add keeps the line where it is an assistant message reporting usage, and
// reports more output than any line of the same message read before it.
func add(l line, where, subagent string, messages map[string]Message) {
	usage := l.Message.Usage
	if l.Type != "assistant" || usage == nil {
		return
	}
	written, err := time.Parse(time.RFC3339Nano, l.Timestamp)
	if err != nil {
		return
	}
	key := l.Message.ID
	if key == "" {
		key = where
	}
	if had, ok := messages[key]; ok && had.Tokens.Output >= usage.Output {
		return
	}
	messages[key] = Message{
		Key:       key,
		WrittenAt: written,
		Model:     l.Message.Model,
		Subagent:  subagent,
		Tokens: Tokens{
			UncachedInput: usage.Input,
			CacheWrite:    usage.CacheCreation,
			CacheRead:     usage.CacheRead,
			Output:        usage.Output,
		},
	}
}

// spawnedAgent is the subagent a tool result says it started, or empty.
func spawnedAgent(raw json.RawMessage) string {
	var result struct {
		AgentID string `json:"agentId"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &result) != nil {
		return ""
	}
	return result.AgentID
}
