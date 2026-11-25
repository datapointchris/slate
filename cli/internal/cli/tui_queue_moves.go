package cli

import (
	"fmt"
	"slices"

	"github.com/datapointchris/slate/cli/internal/api"
)

// queueMove is where < or > moves a queued row: beside which record, and why to ask first.
// Refused says why the API's move rules move it nowhere, and the key then writes nothing.
type queueMove struct {
	to      queueNeighbor
	reasons []string
	refused string
}

// queueMoveFor is where the row at `at` in read's queue moves, ahead of its neighbor or behind
// it, by the rules that `placement` in the API's services/queue_moves.py holds. shown counts the
// queued rows drawn under the scope, so a move past one beyond them asks first.
//
//   - A row raised by a waiter moves only past another row raised by that waiter.
//   - An issue inside an initiative moves only past the nearest issue of that initiative.
//   - Any other row moves past the nearest slot of its own priority and pin: an idea, an issue in
//     no initiative, or an initiative with its issues. A raised row moves with its waiter, so a
//     move steps past it to the slot holding that waiter.
func queueMoveFor(read api.QueueBoard, shown, at int, ahead bool) queueMove {
	row := read.Queue[at]
	step, side := 1, "below"
	if ahead {
		step, side = -1, "above"
	}
	switch {
	case row.RaisedBy != nil:
		return raisedRowMove(read, shown, at, step, side)
	case row.PlacedBy == "initiative" && row.Initiative != nil:
		return insideRowMove(read, shown, at, step, side)
	}
	return slotMove(read, shown, at, step)
}

func raisedRowMove(read api.QueueBoard, shown, at, step int, side string) queueMove {
	rows, row := read.Queue, read.Queue[at]
	waiter := *row.RaisedBy
	next := at + step
	if next < 0 || next >= len(rows) || rows[next].RaisedBy == nil || *rows[next].RaisedBy != waiter {
		return queueMove{refused: fmt.Sprintf("%d moves with %s, which waits on it, since no other issue raised by %d sits %s it",
			row.Number, recordName(read, waiter), waiter, side)}
	}
	to := rowNeighbor(rows, next, shown)
	return queueMove{to: to, reasons: askFirst(read, shown, to, nil)}
}

func insideRowMove(read api.QueueBoard, shown, at, step int, side string) queueMove {
	rows, row := read.Queue, read.Queue[at]
	initiative := row.Initiative
	for next := at + step; next >= 0 && next < len(rows); next += step {
		beside := rows[next]
		if beside.RaisedBy != nil {
			continue
		}
		if beside.Kind != api.RecordKindIssue || slotOf(beside) != initiative.Number {
			break
		}
		to := rowNeighbor(rows, next, shown)
		return queueMove{to: to, reasons: askFirst(read, shown, to, between(at, next, step))}
	}
	return queueMove{refused: fmt.Sprintf("%d sits inside initiative %d %s, so it moves only among that initiative's issues, and none sits %s it",
		row.Number, initiative.Number, initiative.Name, side)}
}

func slotMove(read api.QueueBoard, shown, at, step int) queueMove {
	rows, row := read.Queue, read.Queue[at]
	own := slotOf(row)
	inside := func(at, slot int) bool {
		return at >= 0 && at < len(rows) && (slotOf(rows[at]) == slot || slotOf(rows[at]) == 0)
	}
	next := at + step
	for inside(next, own) {
		next += step
	}
	if next < 0 || next >= len(rows) {
		edge := "last"
		if step < 0 {
			edge = "first"
		}
		return queueMove{refused: fmt.Sprintf("%d is %s in the queue already", row.Number, edge)}
	}
	beside := rows[next]
	slot := slotOf(beside)
	switch {
	case beside.Pinned != row.Pinned:
		pinned, unpinned := row.Number, slot
		if beside.Pinned {
			pinned, unpinned = slot, row.Number
		}
		return queueMove{refused: fmt.Sprintf("%d is pinned and %d is not, and only P moves a row past the pin", pinned, unpinned)}
	case beside.Priority != row.Priority:
		return queueMove{refused: fmt.Sprintf("%d sorts at %s and %d at %s, and the queue reorders only within a priority",
			row.Number, priorityName(row.Priority), slot, priorityName(beside.Priority))}
	}
	passed := []int{next}
	for far := next + step; inside(far, slot); far += step {
		passed = append(passed, far)
	}
	to := slotNeighbor(read, slot, next, shown)
	return queueMove{to: to, reasons: askFirst(read, shown, to, passed)}
}

// slotOf is the record whose slot holds the row's place. An API older than the board's slot
// answers none, so a row standing alone or inside an initiative names its slot from what placed
// it, and a raised row reads 0, which a move steps past.
func slotOf(row api.QueueBoardRow) int {
	switch {
	case row.Slot != nil:
		return *row.Slot
	case row.RaisedBy != nil:
		return 0
	case row.PlacedBy == "initiative" && row.Initiative != nil:
		return row.Initiative.Number
	}
	return row.Number
}

func rowNeighbor(rows []api.QueueBoardRow, at, shown int) queueNeighbor {
	return queueNeighbor{number: rows[at].Number, title: rows[at].Title, priority: rows[at].Priority, hidden: at >= shown}
}

// slotNeighbor names the slot that a move ranks the row beside. The slot's own row places it where
// the queue holds one, and otherwise its row at reached does, the first that the move reaches.
func slotNeighbor(read api.QueueBoard, slot, reached, shown int) queueNeighbor {
	at := slices.IndexFunc(read.Queue, func(row api.QueueBoardRow) bool { return row.Number == slot })
	if at < 0 {
		at = reached
	}
	return queueNeighbor{number: slot, title: recordTitle(read, slot), priority: read.Queue[reached].Priority, hidden: at >= shown}
}

// between is each place strictly between from and to, walking by step.
func between(from, to, step int) []int {
	var places []int
	for at := from + step; at != to; at += step {
		places = append(places, at)
	}
	return places
}

// askFirst says why a move asks before it writes: the record that it lands beside, or a row that
// it passes, sits past the scope, so the screen does not draw what the move changes.
func askFirst(read api.QueueBoard, shown int, to queueNeighbor, passed []int) []string {
	var reasons []string
	if to.hidden {
		reasons = append(reasons, fmt.Sprintf("%d %s is hidden by the scope", to.number, to.title))
	}
	var hidden []queueNeighbor
	for _, at := range passed {
		if at >= shown && read.Queue[at].Number != to.number {
			hidden = append(hidden, rowNeighbor(read.Queue, at, shown))
		}
	}
	if len(hidden) > 0 {
		reasons = append(reasons, "it also passes "+namedRecords(hidden)+", which the scope hides")
	}
	return reasons
}

// recordTitle is a record's title, where a row on the board names it, and empty otherwise.
func recordTitle(read api.QueueBoard, number int) string {
	for _, row := range slices.Concat(read.Queue, read.Claimed) {
		switch {
		case row.Number == number:
			return row.Title
		case row.Initiative != nil && row.Initiative.Number == number:
			return row.Initiative.Name
		}
	}
	return ""
}
