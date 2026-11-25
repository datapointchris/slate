package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/gotermboard"

	"github.com/datapointchris/slate/cli/internal/api"
)

// queuePlace is a row's neighbors in the order its group draws them, for the reorder keys.
type queuePlace struct {
	kind   string
	number int
	above  *queueStep
	below  *queueStep
}

// queueStep is a move past the row drawn beside another. Passes is what the queue holds
// between the two, which the screen does not draw between them.
type queueStep struct {
	to     queueNeighbor
	passes []queueNeighbor
}

// queueNeighbor is a record a row moves among. Hidden says the scope keeps it off the screen.
type queueNeighbor struct {
	number int
	title  string
	hidden bool
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

// Receive is gotermboard.ReceivingBoard's. A write on either writing tab changes the queue, so
// its end reads the board again. A program the board handed the terminal to may have changed
// what it shows, so its handback reads the board again, before the model runs the key's own
// then. A Claude session that held the terminal reads it again the same way.
func (b *initiativesBoard) Receive(m *gotermboard.Model, msg tea.Msg) tea.Cmd {
	sayLinkOpened(m, msg)
	if b.writer.ended(m, msg) {
		b.readAgain()
		return nil
	}
	switch msg := msg.(type) {
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
func (b *initiativesBoard) UnderWay() string { return b.writer.writing }

// readAgain reads the board, its text and the cursor's row on the next tick.
func (b *initiativesBoard) readAgain() {
	b.initiatives.Force()
	b.text.Force()
	b.cursorRow.Force()
}

// openDescriptionElsewhere is a reason to ask before a write on number: another row's
// description is open on screen, so the reader may be looking at that row instead.
func (b *initiativesBoard) openDescriptionElsewhere(number int) []string {
	for open, isOpen := range b.described {
		if isOpen && open != number {
			return []string{fmt.Sprintf("the description open on screen is %d's", open)}
		}
	}
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
			return b.writer.write(m, fmt.Sprintf("moving %d to %s", issue.Number, typed), func(ctx context.Context) (string, error) {
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

// pin is P: it pins the row, or unpins it where it is pinned, through the writer both writing
// tabs hold.
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
	return b.writer.pin(m, placedRecord{kind: kind, number: number, title: title}, pinned, b.openDescriptionElsewhere(number))
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
// drawn below, through the writer both writing tabs hold.
func (b *initiativesBoard) reorder(pursuit int) func(*gotermboard.Model) tea.Cmd {
	return func(m *gotermboard.Model) tea.Cmd {
		place, ok := b.places[m.Cursor()]
		if !ok {
			return nil
		}
		step, edge := place.above, "first"
		if pursuit > 0 {
			step, edge = place.below, "last"
		}
		switch {
		case step == nil:
			m.Say(fmt.Sprintf("%d is %s in its group already", place.number, edge))
			return nil
		case place.kind == "initiative" && b.sort != 0:
			m.Say(fmt.Sprintf("sorted by %s, the rows beside %d are not in the queue's order, so s back to priority reorders it", boardSorts[b.sort].label, place.number))
			return nil
		}
		var reasons []string
		if step.to.hidden {
			reasons = append(reasons, fmt.Sprintf("%d %s is hidden by the scope", step.to.number, step.to.title))
		}
		if len(step.passes) > 0 {
			reasons = append(reasons, "it also passes "+namedRecords(step.passes)+", not drawn between them")
		}
		reasons = append(reasons, b.openDescriptionElsewhere(place.number)...)
		_, _, title, _ := b.recordAt(m.Cursor())
		record := placedRecord{kind: place.kind, number: place.number, title: title}
		return b.writer.reorder(m, record, pursuit < 0, step.to, reasons)
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
