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
	above    *queueNeighbor
	below    *queueNeighbor
}

// queueNeighbor is the record beside a row. Hidden says the scope keeps it off the screen.
type queueNeighbor struct {
	number   int
	title    string
	priority int
	hidden   bool
}

// placeInOrder records each row's neighbors among rows drawn in this order.
func (b *initiativesBoard) placeInOrder(keys []string, places []queuePlace, neighbors []queueNeighbor) {
	for at, key := range keys {
		place := places[at]
		if at > 0 {
			place.above = &neighbors[at-1]
		}
		if at+1 < len(keys) {
			place.below = &neighbors[at+1]
		}
		b.places[key] = place
	}
}

// writeEnded is what a write a key started sends once it ends, for the board that started it.
type writeEnded struct {
	board *initiativesBoard
	said  string
	err   error
}

// handedBack is what a program the board handed the terminal to sends once it exits.
type handedBack struct {
	board *initiativesBoard
	then  func(m *gotermboard.Model) tea.Cmd
}

// Receive is gotermboard.ReceivingBoard's.
func (b *initiativesBoard) Receive(m *gotermboard.Model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case writeEnded:
		if msg.board != b {
			return nil
		}
		b.writing = ""
		if msg.err != nil {
			m.Say(apiSentence(msg.err))
			return nil
		}
		b.readAgain()
		m.Say(msg.said)
	case handedBack:
		if msg.board == b {
			return msg.then(m)
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
		return writeEnded{board: b, said: said, err: err}
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
			var err error
			switch {
			case kind == "issue" && pinned:
				var written api.IssueWritten
				written, err = b.client.UnpinIssue(ctx, ref, issueZone())
				effects = written.WriteEffects
			case kind == "issue":
				var written api.IssueWritten
				written, err = b.client.PinIssue(ctx, ref, pin, issueZone())
				effects = written.WriteEffects
			case pinned:
				var written api.InitiativeWritten
				written, err = b.client.UnpinInitiative(ctx, ref)
				effects = written.WriteEffects
			default:
				var written api.InitiativeWritten
				written, err = b.client.PinInitiative(ctx, ref, pin)
				effects = written.WriteEffects
			}
			if err != nil {
				return "", err
			}
			return withWarnings(fmt.Sprintf("%s %d %s", done, number, title), effects), nil
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
func (b *initiativesBoard) reorder(step int) func(*gotermboard.Model) tea.Cmd {
	return func(m *gotermboard.Model) tea.Cmd {
		place, ok := b.places[m.Cursor()]
		if !ok {
			return nil
		}
		neighbor, side, edge := place.above, "ahead of", "first"
		move := func(ref string) api.RankMove { return api.RankMove{Before: ref} }
		if step > 0 {
			neighbor, side, edge = place.below, "behind", "last"
			move = func(ref string) api.RankMove { return api.RankMove{After: ref} }
		}
		switch {
		case neighbor == nil:
			m.Say(fmt.Sprintf("%d is %s in its group already", place.number, edge))
			return nil
		case place.kind == "initiative" && b.sort != 0:
			m.Say(fmt.Sprintf("sorted by %s, the rows beside %d are not in the queue's order, so s back to priority reorders it", boardSorts[b.sort].label, place.number))
			return nil
		case place.kind == "initiative" && neighbor.priority != place.priority:
			m.Say(fmt.Sprintf("%d is %s and %d is %s, and the queue reorders only within a priority", place.number, priorityName(place.priority), neighbor.number, priorityName(neighbor.priority)))
			return nil
		}
		var reasons []string
		if neighbor.hidden {
			reasons = append(reasons, fmt.Sprintf("%d %s is hidden by the scope", neighbor.number, neighbor.title))
		}
		ref, target := strconv.Itoa(place.number), strconv.Itoa(neighbor.number)
		what := fmt.Sprintf("move %d %s %d", place.number, side, neighbor.number)
		act := func() tea.Cmd {
			return b.write(m, "moving "+strings.TrimPrefix(what, "move "), func(ctx context.Context) (string, error) {
				var effects api.WriteEffects
				if place.kind == "issue" {
					written, err := b.client.RankIssue(ctx, ref, move(target), issueZone())
					if err != nil {
						return "", err
					}
					effects = written.WriteEffects
				} else {
					written, err := b.client.RankInitiative(ctx, ref, move(target), issueZone())
					if err != nil {
						return "", err
					}
					effects = written.WriteEffects
				}
				return withWarnings(fmt.Sprintf("moved %d %s %d %s", place.number, side, neighbor.number, neighbor.title), effects), nil
			})
		}
		return b.confirmFirst(m, what, place.number, reasons, act)
	}
}

func capitalized(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}
