package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/gotermboard"

	"github.com/datapointchris/slate/cli/internal/api"
)

// boardWriteTimeout bounds one write a key starts, retries included.
const boardWriteTimeout = 30 * time.Second

// rankAndPinWriter runs the writes that keys start on the initiatives and queue tabs. Both tabs
// hold the one writer, so a key on either never acts on a queue that a write on the other is
// still changing.
type rankAndPinWriter struct {
	client *api.Client
	// writing is the write under way, which each tab's footer names, or empty. It belongs to the
	// program's goroutine, with the keys.
	writing string
}

func newRankAndPinWriter(client *api.Client) *rankAndPinWriter {
	return &rankAndPinWriter{client: client}
}

// writeEnded is what a write sends once it ends, naming the writer that ran it and the model
// whose key started it. Every tab's board receives it.
type writeEnded struct {
	writer *rankAndPinWriter
	model  *gotermboard.Model
	doing  string
	said   string
	err    error
}

// sentence is what the footer says once the write ends. Only an answer under 500 is a
// refusal, because a timeout, a dropped connection or a proxy's 5xx may follow a write that
// landed.
func (ended writeEnded) sentence() string {
	var answered *api.APIError
	switch {
	case ended.err == nil:
		return ended.said
	case errors.As(ended.err, &answered) && answered.StatusCode < 500:
		return apiSentence(ended.err)
	}
	return fmt.Sprintf("%s may or may not have landed, so the board reads again: %v", capitalized(ended.doing), ended.err)
}

// write runs one write off the program's goroutine while the footer names it. One runs at a
// time across every tab holding the writer.
func (w *rankAndPinWriter) write(m *gotermboard.Model, doing string, run func(ctx context.Context) (string, error)) tea.Cmd {
	if w.writing != "" {
		m.Say(w.writing + " is still under way, so nothing else was written")
		return nil
	}
	w.writing = doing
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), boardWriteTimeout)
		defer cancel()
		said, err := run(ctx)
		return writeEnded{writer: w, model: m, doing: doing, said: said, err: err}
	}
}

// ended takes msg where this writer ran the write it ends, so the next write may start, and says
// what came of it on the model whose key started it. It answers whether msg was such an end, so
// the board receiving it reads again.
func (w *rankAndPinWriter) ended(m *gotermboard.Model, msg tea.Msg) bool {
	ended, ok := msg.(writeEnded)
	if !ok || ended.writer != w {
		return false
	}
	w.writing = ""
	if ended.model == m {
		m.Say(ended.sentence())
	}
	return true
}

// confirmFirst acts at once where the board names no reason to ask, and otherwise asks first,
// naming each.
func (w *rankAndPinWriter) confirmFirst(m *gotermboard.Model, what string, reasons []string, act func() tea.Cmd) tea.Cmd {
	if len(reasons) == 0 {
		return act()
	}
	m.Ask(gotermboard.Question{
		Text:   capitalized(what) + "? " + capitalized(strings.Join(reasons, ", and ")) + ".",
		Answer: func(string) tea.Cmd { return act() },
		Cancel: func() tea.Cmd {
			m.Say("nothing written")
			return nil
		},
	})
	return nil
}

// placedRecord is the record a key pins or moves, as the board drew it.
type placedRecord struct {
	kind   string
	number int
	title  string
}

// pin pins the record, or unpins it where pinned says it is, through what `slate issues pin`
// and `unpin` and the other kinds' verbs of those names run.
func (w *rankAndPinWriter) pin(m *gotermboard.Model, record placedRecord, pinned bool, reasons []string) tea.Cmd {
	writes, writable := queuePlaceWrites[record.kind]
	if !writable {
		m.Say(fmt.Sprintf("no pin reaches %d, whose kind is %s", record.number, record.kind))
		return nil
	}
	ref, place := strconv.Itoa(record.number), agentPlace(os.Getenv(claudeCodeSessionEnv))
	pin := api.PinInput{Session: place.Session, Host: place.Host}
	verb, done, doing := "pin", "pinned", "pinning"
	if pinned {
		verb, done, doing = "unpin", "unpinned", "unpinning"
	}
	act := func() tea.Cmd {
		return w.write(m, fmt.Sprintf("%s %d", doing, record.number), func(ctx context.Context) (string, error) {
			var effects api.WriteEffects
			var name string
			var err error
			if pinned {
				_, effects, name, err = writes.unpin(ctx, w.client, ref)
			} else {
				_, effects, name, err = writes.pin(ctx, w.client, ref, pin)
			}
			if err != nil {
				return "", err
			}
			return withWarnings(done+" "+name, effects), nil
		})
	}
	return w.confirmFirst(m, fmt.Sprintf("%s %d %s", verb, record.number, record.title), reasons, act)
}

// reorder moves the record directly ahead of the neighbor, or behind it, through what `slate
// issues reorder` and the other kinds' verbs of that name run.
func (w *rankAndPinWriter) reorder(m *gotermboard.Model, record placedRecord, ahead bool, neighbor queueNeighbor, reasons []string) tea.Cmd {
	writes, writable := queuePlaceWrites[record.kind]
	if !writable {
		m.Say(fmt.Sprintf("no reorder reaches %d, whose kind is %s", record.number, record.kind))
		return nil
	}
	side, move := "behind", func(ref string) api.RankMove { return api.RankMove{After: ref} }
	if ahead {
		side, move = "ahead of", func(ref string) api.RankMove { return api.RankMove{Before: ref} }
	}
	ref, target := strconv.Itoa(record.number), strconv.Itoa(neighbor.number)
	what := fmt.Sprintf("move %d %s %d", record.number, side, neighbor.number)
	act := func() tea.Cmd {
		return w.write(m, "moving "+strings.TrimPrefix(what, "move "), func(ctx context.Context) (string, error) {
			_, effects, name, err := writes.rank(ctx, w.client, ref, move(target))
			if err != nil {
				return "", err
			}
			return withWarnings(strings.TrimSpace(fmt.Sprintf("moved %s %s %d %s", name, side, neighbor.number, neighbor.title)), effects), nil
		})
	}
	return w.confirmFirst(m, what, reasons, act)
}

// apiSentence is what the API said where it refused, rather than the status line around it.
func apiSentence(err error) string {
	var refused *api.APIError
	if errors.As(err, &refused) && refused.Message != "" {
		return refused.Message
	}
	return err.Error()
}

// withWarnings is said with each warning the write went through despite.
func withWarnings(said string, effects api.WriteEffects) string {
	for _, warning := range effects.Warnings {
		said += "; " + warning.Message
	}
	return said
}

func capitalized(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}
