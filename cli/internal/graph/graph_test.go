package graph

import (
	"strings"
	"testing"
)

// rowIDs renders the walk as "id@depth" so a failure names the shape that came
// back rather than a struct dump.
func rowIDs(rows []Row) string {
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		id := r.ID
		if r.Repeated {
			id += "(*)"
		}
		parts = append(parts, id)
	}
	return strings.Join(parts, " ")
}

// a → b means a depends on b.
func diamond() *Graph {
	return Build([]Node{
		{ID: "a", Order: 1, Deps: []string{"b", "c"}},
		{ID: "b", Order: 2, Deps: []string{"d"}},
		{ID: "c", Order: 3, Deps: []string{"d"}},
		{ID: "d", Order: 4, Deps: []string{"e"}},
		{ID: "e", Order: 5},
	})
}

func TestRows_SharedDependencyIsExpandedOnceThenMarked(t *testing.T) {
	g := diamond()

	rows := g.Rows(g.Roots([]string{"a", "b", "c", "d", "e"}, false), false, Unlimited)

	want := "a b d e c d(*)"
	if got := rowIDs(rows); got != want {
		t.Errorf("rows = %q, want %q — the second d keeps its row and loses its subtree", got, want)
	}
}

func TestRows_ARepeatedLeafIsNotMarkedBecauseNothingWasElided(t *testing.T) {
	g := Build([]Node{
		{ID: "a", Order: 1, Deps: []string{"b", "c"}},
		{ID: "b", Order: 2, Deps: []string{"d"}},
		{ID: "c", Order: 3, Deps: []string{"d"}},
		{ID: "d", Order: 4},
	})

	rows := g.Rows([]string{"a"}, false, Unlimited)

	if got := rowIDs(rows); strings.Contains(got, "(*)") {
		t.Errorf("rows = %q, want no marker — d has no children to hide", got)
	}
}

func TestRows_LastFlagsPlaceTheCorners(t *testing.T) {
	g := diamond()

	rows := g.Rows([]string{"a"}, false, Unlimited)

	byID := map[string][]bool{}
	for _, r := range rows {
		if _, seen := byID[r.ID]; !seen {
			byID[r.ID] = r.Last
		}
	}
	if got := byID["b"]; len(got) != 1 || got[0] {
		t.Errorf("b.Last = %v, want [false] — b has a sibling below it", got)
	}
	if got := byID["c"]; len(got) != 1 || !got[0] {
		t.Errorf("c.Last = %v, want [true] — c is the last child of a", got)
	}
	if got := byID["a"]; len(got) != 0 {
		t.Errorf("a.Last = %v, want empty — a root draws no prefix", got)
	}
}

func TestRows_ParentIsTheItemTheRowHangsUnder(t *testing.T) {
	g := diamond()

	rows := g.Rows([]string{"a"}, false, Unlimited)

	for _, r := range rows {
		switch r.ID {
		case "a":
			if r.Parent != "" {
				t.Errorf("root parent = %q, want empty", r.Parent)
			}
		case "e":
			if r.Parent != "d" {
				t.Errorf("e.Parent = %q, want d", r.Parent)
			}
		}
	}
}

func TestRoots_InvertSwapsWhichEndIsTheRoot(t *testing.T) {
	g := diamond()
	members := []string{"a", "b", "c", "d", "e"}

	forward := g.Roots(members, false)
	inverted := g.Roots(members, true)

	if len(forward) != 1 || forward[0] != "a" {
		t.Errorf("forward roots = %v, want [a] — nothing depends on a", forward)
	}
	if len(inverted) != 1 || inverted[0] != "e" {
		t.Errorf("inverted roots = %v, want [e] — e depends on nothing", inverted)
	}
}

// The measured case this exists for: six items queued behind one, where the
// forward drawing repeats the shared node five times and the inverted one says
// it once.
func TestRows_InvertCollapsesAFanIntoOneTree(t *testing.T) {
	nodes := []Node{{ID: "keystone", Order: 1}}
	for _, id := range []string{"w", "x", "y", "z"} {
		nodes = append(nodes, Node{ID: id, Order: len(nodes) + 1, Deps: []string{"keystone"}})
	}
	g := Build(nodes)
	members := []string{"keystone", "w", "x", "y", "z"}

	forward := g.Rows(g.Roots(members, false), false, Unlimited)
	inverted := g.Rows(g.Roots(members, true), true, Unlimited)

	if len(forward) != 8 {
		t.Errorf("forward rows = %d (%s), want 8 — four roots each carrying keystone", len(forward), rowIDs(forward))
	}
	if len(inverted) != 5 {
		t.Errorf("inverted rows = %d (%s), want 5 — one root and its four dependents", len(inverted), rowIDs(inverted))
	}
}

func TestComponents_SeparatesUnconnectedTreesAndDropsIsolatedItems(t *testing.T) {
	g := Build([]Node{
		{ID: "a", Order: 1, Deps: []string{"b"}},
		{ID: "b", Order: 2},
		{ID: "lonely", Order: 3},
		{ID: "c", Order: 4, Deps: []string{"d"}},
		{ID: "d", Order: 5},
	})

	components := g.Components()

	if len(components) != 2 {
		t.Fatalf("components = %v, want 2 — an item with no edges is in no tree", components)
	}
	for _, members := range components {
		for _, id := range members {
			if id == "lonely" {
				t.Errorf("components = %v, want lonely left out", components)
			}
		}
	}
}

func TestComponents_OrderFollowsTheItemNumberNotMapIteration(t *testing.T) {
	g := Build([]Node{
		{ID: "high", Order: 90, Deps: []string{"high-dep"}},
		{ID: "high-dep", Order: 91},
		{ID: "low", Order: 2, Deps: []string{"low-dep"}},
		{ID: "low-dep", Order: 3},
	})

	first := g.Components()
	for i := 0; i < 20; i++ {
		if got := g.Components(); got[0][0] != first[0][0] {
			t.Fatalf("components reordered between runs: %v then %v", first, got)
		}
	}
	if first[0][0] != "low" {
		t.Errorf("first component starts at %q, want low — the lowest number leads", first[0][0])
	}
}

func TestBuild_DropsAnEdgeToAnItemThatWasNotFetched(t *testing.T) {
	g := Build([]Node{{ID: "a", Order: 1, Deps: []string{"missing"}}})

	if g.Connected("a") {
		t.Error("a is connected, want isolated — an edge to an unfetched item draws a row with no title")
	}
	if rows := g.Rows([]string{"a"}, false, Unlimited); len(rows) != 1 {
		t.Errorf("rows = %s, want just a", rowIDs(rows))
	}
}

func TestRows_DepthStopsBelowTheLimit(t *testing.T) {
	g := diamond()

	rows := g.Rows([]string{"a"}, false, 1)

	if got := rowIDs(rows); got != "a b c" {
		t.Errorf("rows at depth 1 = %q, want %q", got, "a b c")
	}
	if rows := g.Rows([]string{"a"}, false, 0); len(rows) != 1 {
		t.Errorf("rows at depth 0 = %s, want the root alone", rowIDs(rows))
	}
}

// The API refuses a cycle, so this guards against bad data rather than a shape
// anyone can create. It must terminate, not overflow the stack.
func TestRows_ACycleTerminatesInsteadOfRecursingForever(t *testing.T) {
	g := Build([]Node{
		{ID: "a", Order: 1, Deps: []string{"b"}},
		{ID: "b", Order: 2, Deps: []string{"a"}},
	})

	rows := g.Rows(g.Roots([]string{"a", "b"}, false), false, Unlimited)

	if len(rows) == 0 || len(rows) > 4 {
		t.Errorf("rows = %s, want a short marked walk", rowIDs(rows))
	}
	if !strings.Contains(rowIDs(rows), "(*)") {
		t.Errorf("rows = %s, want the closing edge marked", rowIDs(rows))
	}
}

func TestRoots_AnAllCycleComponentStillDraws(t *testing.T) {
	g := Build([]Node{
		{ID: "a", Order: 1, Deps: []string{"b"}},
		{ID: "b", Order: 2, Deps: []string{"a"}},
	})

	roots := g.Roots([]string{"a", "b"}, false)

	if len(roots) != 1 || roots[0] != "a" {
		t.Errorf("roots = %v, want [a] — no true root, so the lowest member stands in", roots)
	}
}

func TestEdges_StayInTheStoredDirection(t *testing.T) {
	g := diamond()

	edges := g.Edges([]string{"a", "b", "c", "d", "e"})

	for _, e := range edges {
		if e[0] == "e" {
			t.Errorf("edges = %v, want none starting at e — e depends on nothing", edges)
		}
	}
	if len(edges) != 5 {
		t.Errorf("edges = %d, want 5", len(edges))
	}
}

func TestComponentOf_ReturnsNilForAnItemWithNoEdges(t *testing.T) {
	g := Build([]Node{{ID: "lonely", Order: 1}, {ID: "a", Order: 2, Deps: []string{"b"}}, {ID: "b", Order: 3}})

	if members := g.ComponentOf("lonely"); members != nil {
		t.Errorf("ComponentOf(lonely) = %v, want nil", members)
	}
	if members := g.ComponentOf("b"); len(members) != 2 {
		t.Errorf("ComponentOf(b) = %v, want both ends of the edge", members)
	}
}
