// Package graph turns dependency edges into the rows of a drawn tree.
//
// It knows nothing about items or the API: a caller supplies ids, a sort key,
// and the ids each node depends on, and gets back flattened rows carrying the
// depth and box-drawing state a renderer needs. That split is what lets the
// tree shape be tested without a server.
package graph

import "sort"

// Node is one item as the tree builder sees it. Order is the stable sort key
// for siblings and roots — the item number, so a drawing is reproducible
// between runs rather than following map iteration.
type Node struct {
	ID    string
	Order int
	Deps  []string
}

// Row is one line of a drawn tree.
//
// Last carries an is-last-child flag per ancestor level, which is what decides
// whether a level continues with "│" or clears to spaces. A renderer cannot
// derive it from Depth alone, and threading it back out of the walk is cheaper
// than a second pass.
type Row struct {
	ID    string
	Depth int
	Last  []bool
	// Parent is the node this row hangs under, empty at a root. The walk knows
	// it and a renderer cannot recover it from the flattened rows, since the
	// same node appears under several parents.
	Parent string
	// Repeated marks a node whose children were already drawn under an earlier
	// parent. Only a node that actually has children is ever marked, because
	// the marker's job is to say something was elided.
	Repeated bool
}

// Unlimited is the depth that draws every level.
const Unlimited = -1

type Graph struct {
	nodes map[string]Node
	deps  map[string][]string
	users map[string][]string
	ids   []string
}

// Build indexes the nodes both ways. An edge naming an id that is not in nodes
// is dropped: the caller may have fetched a subset, and a dangling edge would
// otherwise draw a row with no title.
func Build(nodes []Node) *Graph {
	g := &Graph{
		nodes: make(map[string]Node, len(nodes)),
		deps:  make(map[string][]string),
		users: make(map[string][]string),
		ids:   make([]string, 0, len(nodes)),
	}
	for _, n := range nodes {
		g.nodes[n.ID] = n
		g.ids = append(g.ids, n.ID)
	}
	for _, n := range nodes {
		for _, dep := range n.Deps {
			if _, ok := g.nodes[dep]; !ok {
				continue
			}
			g.deps[n.ID] = append(g.deps[n.ID], dep)
			g.users[dep] = append(g.users[dep], n.ID)
		}
	}
	g.sortByOrder(g.ids)
	for id := range g.deps {
		g.sortByOrder(g.deps[id])
	}
	for id := range g.users {
		g.sortByOrder(g.users[id])
	}
	return g
}

func (g *Graph) sortByOrder(ids []string) {
	sort.Slice(ids, func(i, j int) bool {
		a, b := g.nodes[ids[i]], g.nodes[ids[j]]
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.ID < b.ID
	})
}

// Connected reports whether an id has any edge at all. An item with no
// dependencies and no dependents is not part of any tree.
func (g *Graph) Connected(id string) bool {
	return len(g.deps[id]) > 0 || len(g.users[id]) > 0
}

// Components returns the weakly connected components, each Order-sorted, the
// components themselves ordered by their lowest member. Isolated nodes are left
// out — a tree of one has no shape to show.
func (g *Graph) Components() [][]string {
	parent := make(map[string]string, len(g.ids))
	var find func(string) string
	find = func(x string) string {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	for _, id := range g.ids {
		parent[id] = id
	}
	for _, id := range g.ids {
		for _, dep := range g.deps[id] {
			if a, b := find(id), find(dep); a != b {
				parent[a] = b
			}
		}
	}

	grouped := make(map[string][]string)
	for _, id := range g.ids {
		if !g.Connected(id) {
			continue
		}
		root := find(id)
		grouped[root] = append(grouped[root], id)
	}

	components := make([][]string, 0, len(grouped))
	for _, members := range grouped {
		g.sortByOrder(members)
		components = append(components, members)
	}
	sort.Slice(components, func(i, j int) bool {
		a, b := g.nodes[components[i][0]], g.nodes[components[j][0]]
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.ID < b.ID
	})
	return components
}

// ComponentOf returns the component holding id, or nil when the item has no
// edges.
func (g *Graph) ComponentOf(id string) []string {
	for _, members := range g.Components() {
		for _, member := range members {
			if member == id {
				return members
			}
		}
	}
	return nil
}

// Roots returns the members nothing else in the drawing direction points at.
//
// Inverted, an edge is read from the thing depended on toward the things it
// unblocks, so the roots swap ends with it. A component that is entirely a
// cycle has no root at all, and returning nothing there would silently drop
// items from the drawing, so its lowest member stands in.
func (g *Graph) Roots(members []string, invert bool) []string {
	// A row's children are the things it waits on, so the thing pointing AT a
	// node is whatever depends on it. Inverted, the drawing runs the other way
	// and the two swap.
	incoming := g.users
	if invert {
		incoming = g.deps
	}
	inComponent := make(map[string]bool, len(members))
	for _, id := range members {
		inComponent[id] = true
	}

	var roots []string
	for _, id := range members {
		pointed := false
		for _, other := range incoming[id] {
			if inComponent[other] {
				pointed = true
				break
			}
		}
		if !pointed {
			roots = append(roots, id)
		}
	}
	if len(roots) == 0 && len(members) > 0 {
		roots = []string{members[0]}
	}
	g.sortByOrder(roots)
	return roots
}

// Rows walks the trees under roots and flattens them into drawable lines.
//
// A node expanded once is not expanded again: the second appearance is marked
// Repeated and its children are left off, which is what keeps a shared
// dependency from multiplying the drawing. maxDepth of Unlimited draws every
// level; 0 draws the roots alone.
func (g *Graph) Rows(roots []string, invert bool, maxDepth int) []Row {
	children := g.deps
	if invert {
		children = g.users
	}
	expanded := make(map[string]bool)
	ancestors := make(map[string]bool)
	var rows []Row

	var walk func(id, parent string, last []bool)
	walk = func(id, parent string, last []bool) {
		depth := len(last)
		kids := children[id]
		// An ancestor reappearing is a cycle. The API refuses to create one, so
		// this is a guard against bad data rather than an expected shape — draw
		// the row, mark it, and stop before recursing forever.
		repeated := (expanded[id] || ancestors[id]) && len(kids) > 0
		rows = append(rows, Row{ID: id, Depth: depth, Last: append([]bool(nil), last...), Parent: parent, Repeated: repeated})
		if repeated || len(kids) == 0 {
			return
		}
		if maxDepth != Unlimited && depth >= maxDepth {
			return
		}
		expanded[id] = true
		ancestors[id] = true
		for i, kid := range kids {
			next := make([]bool, len(last)+1)
			copy(next, last)
			next[len(last)] = i == len(kids)-1
			walk(kid, id, next)
		}
		ancestors[id] = false
	}

	for _, root := range roots {
		walk(root, "", nil)
	}
	return rows
}

// Edges returns every edge in the canonical stored direction — the item first,
// the thing it depends on second — whichever way the drawing runs. Inverting is
// a choice about rendering, and re-pointing the edges to match would hand a
// consumer a graph that contradicts the one the API holds.
func (g *Graph) Edges(members []string) [][2]string {
	inSet := make(map[string]bool, len(members))
	for _, id := range members {
		inSet[id] = true
	}
	var edges [][2]string
	for _, id := range members {
		for _, dep := range g.deps[id] {
			if inSet[dep] {
				edges = append(edges, [2]string{id, dep})
			}
		}
	}
	return edges
}
