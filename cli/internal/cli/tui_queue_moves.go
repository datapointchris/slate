package cli

import (
	"fmt"
	"slices"

	"github.com/datapointchris/slate/cli/internal/api"
)

// queueMove is where < or > moves a queued row: beside which record, and why to ask first.
// Refused is why the API's rank write would move it nowhere, and the key then writes nothing.
type queueMove struct {
	to      queueNeighbor
	reasons []string
	refused string
}

// queueMoveFor is where the board says the row at `at` moves, ahead or behind. The API answers
// each queued row's moves by its rank write's own rules, so the tab looks the move up. shown
// counts the queued rows drawn under the scope, so a move beside or past one beyond them asks first.
func queueMoveFor(read api.QueueBoard, shown, at int, ahead bool) queueMove {
	row := read.Queue[at]
	if row.Moves == nil {
		return queueMove{refused: fmt.Sprintf("this API answers no moves for %d, so it is older than this CLI", row.Number)}
	}
	move := row.Moves.Behind
	if ahead {
		move = row.Moves.Ahead
	}
	switch {
	case move.Beside == nil && move.Refused == "":
		return queueMove{refused: fmt.Sprintf("the API names no record for %d to move beside", row.Number)}
	case move.Beside == nil:
		return queueMove{refused: move.Refused}
	}
	to := boardNeighbor(read, *move.Beside, shown)
	return queueMove{to: to, reasons: askFirst(read, shown, to, move.Passes)}
}

// boardNeighbor names the move's beside record. Where the queue holds no row of its own for it,
// as for an initiative drawn only through its issues, its title comes from a row naming it, and
// the rows that the move passes say whether the scope hides them.
func boardNeighbor(read api.QueueBoard, number, shown int) queueNeighbor {
	if at := queueIndex(read, number); at >= 0 {
		return rowNeighbor(read.Queue, at, shown)
	}
	return queueNeighbor{number: number, title: recordTitle(read, number)}
}

func queueIndex(read api.QueueBoard, number int) int {
	return slices.IndexFunc(read.Queue, func(row api.QueueBoardRow) bool { return row.Number == number })
}

func rowNeighbor(rows []api.QueueBoardRow, at, shown int) queueNeighbor {
	return queueNeighbor{number: rows[at].Number, title: rows[at].Title, hidden: at >= shown}
}

// askFirst says why a move asks before it writes: the record that it lands beside, or a row that
// it passes, sits past the scope, so the screen does not draw what the move changes.
func askFirst(read api.QueueBoard, shown int, to queueNeighbor, passes []int) []string {
	var reasons []string
	if to.hidden {
		reasons = append(reasons, fmt.Sprintf("%d %s is hidden by the scope", to.number, to.title))
	}
	var hidden []queueNeighbor
	for _, number := range passes {
		if at := queueIndex(read, number); at >= shown && number != to.number {
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
