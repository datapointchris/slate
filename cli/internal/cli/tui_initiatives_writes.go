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

// queuePlace is a row's neighbors in the order its group draws them, for the reorder keys.
type queuePlace struct {
	kind     string
	number   int
	priority int
	above    *queueStep
	below    *queueStep
}

// queueStep is a move past the row drawn beside another. Passes is what the queue holds
// between the two, which the screen does not draw between them.
type queueStep struct {
	to     queueNeighbor
	passes []queueNeighbor
}

// queueNeighbor is a record a row moves among. Hidden says the scope keeps it off the screen.
type queueNeighbor struct {
	number   int
	title    string
	priority int
	hidden   bool
}

// placeInOrder records each row's neighbors among the rows drawn, in drawn order, with what
// queue, every record they move among in the queue's order, holds between each pair.
func (b *initiativesBoard) placeInOrder(keys []string, places []queuePlace, drawn, queue []queueNeighbor) {
	positions := make(map[int]int, len(queue))
	for position, record := range queue {
		positions[record.number] = position
	}
	step := func(from, to queueNeighbor) *queueStep {
		low, lowKnown := positions[from.number]
		high, highKnown := positions[to.number]
		if !lowKnown || !highKnown {
			return &queueStep{to: to}
		}
		if low > high {
			low, high = high, low
		}
		return &queueStep{to: to, passes: queue[low+1 : high]}
	}
	for at, key := range keys {
		place := places[at]
		if at > 0 {
			place.above = step(drawn[at], drawn[at-1])
		}
		if at+1 < len(keys) {
			place.below = step(drawn[at], drawn[at+1])
		}
		b.places[key] = place
	}
}

// writeEnded is what a write a key started sends once it ends, for the board that started it.
type writeEnded struct {
	board *initiativesBoard
	doing string
	said  string
	err   error
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

// Receive is gotermboard.ReceivingBoard's. A program the board handed the terminal to may have
// changed what it shows, so its handback reads the board again, before the model runs the
// key's own then. A Claude session that held the terminal reads it again the same way.
func (b *initiativesBoard) Receive(m *gotermboard.Model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case writeEnded:
		if msg.board != b {
			return nil
		}
		b.writing = ""
		b.readAgain()
		m.Say(msg.sentence())
	case gotermboard.HandedBack:
		b.readAgain()
	case gotermboard.ClaudeSessionOutcome:
		if msg.HeldTerminal {
			b.readAgain()
		}
	}
	return nil
}

// UnderWay is gotermboard.BusyBoard's.
func (b *initiativesBoard) UnderWay() string { return b.writing }

// readAgain reads the board, its text and the cursor's row on the next tick.
func (b *initiativesBoard) readAgain() {
	b.initiatives.Force()
	b.text.Force()
	b.cursorRow.Force()
}

// write runs one write off the program's goroutine while the footer names it. One runs at
// a time, so a second key never acts on a board the first is still changing.
func (b *initiativesBoard) write(m *gotermboard.Model, doing string, run func(ctx context.Context) (string, error)) tea.Cmd {
	if b.writing != "" {
		m.Say(b.writing + " is still under way, so nothing else was written")
		return nil
	}
	b.writing = doing
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), boardWriteTimeout)
		defer cancel()
		said, err := run(ctx)
		return writeEnded{board: b, doing: doing, said: said, err: err}
	}
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

// confirmFirst acts at once where nothing on screen points at another row, and otherwise
// asks first, naming why.
func (b *initiativesBoard) confirmFirst(m *gotermboard.Model, what string, number int, reasons []string, act func() tea.Cmd) tea.Cmd {
	for open, isOpen := range b.described {
		if isOpen && open != number {
			reasons = append(reasons, fmt.Sprintf("the description open on screen is %d's", open))
			break
		}
	}
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

// move is M: it asks which initiative, then runs what `slate issues edit --initiative` runs.
func (b *initiativesBoard) move(m *gotermboard.Model) tea.Cmd {
	issue, ok := b.issueAt(m.Cursor())
	if !ok {
		return nil
	}
	m.Ask(gotermboard.Question{
		Text:  fmt.Sprintf("Move %d %s to which initiative? Its number or name.", issue.Number, issue.Title),
		Typed: true,
		Answer: func(typed string) tea.Cmd {
			if typed == "" {
				m.Say("nothing moved, since no initiative was named")
				return nil
			}
			ref := strconv.Itoa(issue.Number)
			return b.write(m, fmt.Sprintf("moving %d to %s", issue.Number, typed), func(ctx context.Context) (string, error) {
				written, err := b.client.UpdateIssue(ctx, ref, api.IssueUpdateInput{Initiative: &typed}, nil, issueZone())
				if err != nil {
					return "", err
				}
				where := "no initiative"
				if initiative := written.Issue.Initiative; initiative != nil {
					where = fmt.Sprintf("%d %s", initiative.Number, initiative.Name)
				}
				return withWarnings(fmt.Sprintf("moved %d to %s", issue.Number, where), written.WriteEffects), nil
			})
		},
		Cancel: func() tea.Cmd {
			m.Say("nothing moved")
			return nil
		},
	})
	return nil
}

// pin is P: it pins the row, or unpins it where it is pinned, through what `slate issues
// pin` and `unpin` and the initiatives' verbs of those names run.
func (b *initiativesBoard) pin(m *gotermboard.Model) tea.Cmd {
	kind, number, title, ok := b.recordAt(m.Cursor())
	if !ok {
		return nil
	}
	writes, writable := queuePlaceWrites[kind]
	if !writable {
		m.Say(fmt.Sprintf("no pin reaches %d, whose kind is %s", number, kind))
		return nil
	}
	pinned, known := b.pinned(kind, number)
	if !known {
		m.Say(fmt.Sprintf("%d's pin is still being read, so press P again once its detail shows", number))
		return nil
	}
	ref, place := strconv.Itoa(number), agentPlace(os.Getenv(claudeCodeSessionEnv))
	pin := api.PinInput{Session: place.Session, Host: place.Host}
	verb, done, doing := "pin", "pinned", "pinning"
	if pinned {
		verb, done, doing = "unpin", "unpinned", "unpinning"
	}
	act := func() tea.Cmd {
		return b.write(m, fmt.Sprintf("%s %d", doing, number), func(ctx context.Context) (string, error) {
			var effects api.WriteEffects
			var name string
			var err error
			if pinned {
				_, effects, name, err = writes.unpin(ctx, b.client, ref)
			} else {
				_, effects, name, err = writes.pin(ctx, b.client, ref, pin)
			}
			if err != nil {
				return "", err
			}
			return withWarnings(done+" "+name, effects), nil
		})
	}
	return b.confirmFirst(m, fmt.Sprintf("%s %d %s", verb, number, title), number, nil, act)
}

// pinned says whether the record is pinned, and whether that is known yet. The board read
// carries an initiative's pin, and an issue's comes with the cursor's row.
func (b *initiativesBoard) pinned(kind string, number int) (pinned, known bool) {
	if kind == "issue" {
		issue, read := b.cursorRow.Value().issues[number]
		return issue.PinnedTS != nil, read
	}
	for _, initiative := range b.initiatives.Value().Initiatives {
		if initiative.Number == number {
			return initiative.PinnedTS != nil, true
		}
	}
	return false, false
}

func (b *initiativesBoard) reorderable(m *gotermboard.Model) bool {
	_, ok := b.places[m.Cursor()]
	return ok
}

// reorder is < and >: it moves the row ahead of the row drawn above it, or behind the one
// drawn below, through what `slate issues reorder` and `slate initiatives reorder` run.
func (b *initiativesBoard) reorder(direction int) func(*gotermboard.Model) tea.Cmd {
	return func(m *gotermboard.Model) tea.Cmd {
		place, ok := b.places[m.Cursor()]
		if !ok {
			return nil
		}
		step, side, edge := place.above, "ahead of", "first"
		move := func(ref string) api.RankMove { return api.RankMove{Before: ref} }
		if direction > 0 {
			step, side, edge = place.below, "behind", "last"
			move = func(ref string) api.RankMove { return api.RankMove{After: ref} }
		}
		switch {
		case step == nil:
			m.Say(fmt.Sprintf("%d is %s in its group already", place.number, edge))
			return nil
		case place.kind == "initiative" && b.sort != 0:
			m.Say(fmt.Sprintf("sorted by %s, the rows beside %d are not in the queue's order, so s back to priority reorders it", boardSorts[b.sort].label, place.number))
			return nil
		case place.kind == "initiative" && step.to.priority != place.priority:
			m.Say(fmt.Sprintf("%d is %s and %d is %s, and the queue reorders only within a priority", place.number, priorityName(place.priority), step.to.number, priorityName(step.to.priority)))
			return nil
		}
		writes, writable := queuePlaceWrites[place.kind]
		if !writable {
			m.Say(fmt.Sprintf("no reorder reaches %d, whose kind is %s", place.number, place.kind))
			return nil
		}
		var reasons []string
		if step.to.hidden {
			reasons = append(reasons, fmt.Sprintf("%d %s is hidden by the scope", step.to.number, step.to.title))
		}
		if len(step.passes) > 0 {
			reasons = append(reasons, "it also passes "+namedRecords(step.passes)+", not drawn between them")
		}
		neighbor := step.to
		ref, target := strconv.Itoa(place.number), strconv.Itoa(neighbor.number)
		what := fmt.Sprintf("move %d %s %d", place.number, side, neighbor.number)
		act := func() tea.Cmd {
			return b.write(m, "moving "+strings.TrimPrefix(what, "move "), func(ctx context.Context) (string, error) {
				_, effects, name, err := writes.rank(ctx, b.client, ref, move(target))
				if err != nil {
					return "", err
				}
				return withWarnings(fmt.Sprintf("moved %s %s %d %s", name, side, neighbor.number, neighbor.title), effects), nil
			})
		}
		return b.confirmFirst(m, what, place.number, reasons, act)
	}
}

// namedRecords names the first two records by number and title, and counts the rest.
func namedRecords(records []queueNeighbor) string {
	named := make([]string, 0, 3)
	for _, record := range records[:min(2, len(records))] {
		named = append(named, fmt.Sprintf("%d %s", record.number, record.title))
	}
	if rest := len(records) - len(named); rest > 0 {
		named = append(named, fmt.Sprintf("%d more", rest))
	}
	return strings.Join(named, ", ")
}

func capitalized(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}
