package cli

import (
	"strings"

	"github.com/datapointchris/slate/cli/internal/graph"
)

// The marks a tree row carries for a record's state.
const (
	markOpen      = "○"
	markCompleted = "✓"
	markArchived  = "▪"
)

// drawTree is one drawing: where the walk starts, and the records that bound what
// the machine rendering may carry.
type drawTree struct {
	roots   []string
	members []string
}

// treeForItem roots the drawing at the record that was named, whichever way the
// edges are read. Forward that is what the record waits on, inverted it is what
// finishing it releases; starting from the component's own ends instead would
// answer neither question about the record you asked about. The bool reports
// whether the record has any edge at all.
func treeForItem(g *graph.Graph, id string) (drawTree, bool) {
	members := g.ComponentOf(id)
	if members == nil {
		return drawTree{roots: []string{id}, members: []string{id}}, false
	}
	return drawTree{roots: []string{id}, members: members}, true
}

// treePrefix draws a row's indent: a rail per ancestor with siblings to come, then its branch.
func treePrefix(row graph.Row) string {
	var b strings.Builder
	for i, last := range row.Last {
		switch {
		case i < len(row.Last)-1 && last:
			b.WriteString("    ")
		case i < len(row.Last)-1:
			b.WriteString("│   ")
		case last:
			b.WriteString("└── ")
		default:
			b.WriteString("├── ")
		}
	}
	return b.String()
}
