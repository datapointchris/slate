package cli

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
	"github.com/datapointchris/slate/cli/internal/graph"
)

func newDependenciesTreeCommand() *cobra.Command {
	var (
		asJSON      bool
		invert      bool
		depth       int
		issueStatus string
		repo        string
	)
	cmd := &cobra.Command{
		Use:   "tree [<record>]",
		Short: "Draw the dependency trees, or the one an issue or an initiative sits in",
		Long: "Name an issue or an initiative to draw the tree it belongs to, or nothing to\n" +
			"draw every tree. A record with no dependency either way is in no tree.\n" +
			"\n" +
			"A row is something the row it hangs from waits on. --invert reads the edges\n" +
			"the other way, so a row waits on the row it hangs from. An initiative's row\n" +
			"hangs what it waits on, and each unclosed issue in it hangs the initiative.\n" +
			"\n" +
			"--status and --repo choose which trees are drawn, never which rows. A tree is\n" +
			"kept when any of its issues matches, and then drawn whole. Dropping a row\n" +
			"would orphan everything below it. Trees with nothing unclosed are hidden by\n" +
			"default, and an unfinished initiative, someday included, counts as unclosed.\n" +
			"\n" +
			"(*) marks a record whose dependencies were drawn higher up.",
		Example: "  slate dependencies tree\n" +
			"  slate dependencies tree 412\n" +
			"  slate dependencies tree 520\n" +
			"  slate dependencies tree 412 --invert\n" +
			"  slate dependencies tree --repo webapp --json",
		Args:        cobra.MaximumNArgs(1),
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("status") && !slices.Contains(api.IssueStatuses, issueStatus) {
				return goclikit.UsageError(fmt.Errorf("unknown status %q — one of: %s", issueStatus, strings.Join(api.IssueStatuses, ", ")))
			}
			if depth < 0 {
				return goclikit.UsageError(fmt.Errorf("--depth cannot be negative"))
			}
			maxDepth := graph.Unlimited
			if cmd.Flags().Changed("depth") {
				maxDepth = depth
			}
			filter, err := repoFlagValue(cmd, repo)
			if err != nil {
				return err
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			// Every issue and initiative, whatever the flags: a closed record
			// mid-chain is what joins the halves either side of it.
			issues, err := client.ListIssues(cmd.Context(), api.IssueFilter{Status: api.IssueStatusAll}, api.Window{}, issueZone(), nil)
			if err != nil {
				return handleAPIError(err)
			}
			initiatives, err := client.ListInitiatives(cmd.Context(), "all", "", nil)
			if err != nil {
				return handleAPIError(err)
			}
			byID := treeRecords(issues, initiatives)
			nodes := make([]graph.Node, 0, len(byID))
			for id, record := range byID {
				nodes = append(nodes, graph.Node{ID: id, Order: record.Number, Deps: record.DependsOn})
			}
			g := graph.Build(nodes)

			var trees []drawTree
			if len(args) == 1 {
				record, err := resolveTreeRecord(byID, args[0])
				if err != nil {
					return err
				}
				tree, connected := treeForItem(g, record.ID)
				if !connected {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "#%d has no dependencies either way.\n", record.Number)
				}
				trees = []drawTree{tree}
			} else {
				for _, members := range g.Components() {
					if treeMatches(members, byID, issueStatus, filter) {
						trees = append(trees, drawTree{roots: g.Roots(members, invert), members: members})
					}
				}
			}

			if len(args) == 0 && !cmd.Flags().Changed("status") {
				defer noteHiddenByStatus(cmd, "Trees with nothing unclosed")
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), buildTreeJSON(g, byID, trees, invert, maxDepth))
			}
			printTrees(cmd.OutOrStdout(), g, byID, trees, invert, maxDepth)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the nodes and edges as JSON to stdout")
	cmd.Flags().BoolVar(&invert, "invert", false, "Draw what each record releases instead of what it waits on")
	cmd.Flags().IntVar(&depth, "depth", 0, "Stop drawing below this many levels (default every level)")
	cmd.Flags().StringVar(&repo, "repo", "", "Only trees holding an issue on this repo (empty string for issues on no repo)")
	cmd.Flags().StringVar(&issueStatus, "status", "", "Only trees holding an issue in this state: "+strings.Join(api.IssueStatuses, ", ")+" (default unclosed)")
	withFlagValues(cmd, "status", api.IssueStatuses...)
	return cmd
}

// treeRecord is an issue or an initiative as a tree draws it, keyed by its
// number, which the two kinds share one sequence of.
type treeRecord struct {
	ID        string
	UUID      string
	Kind      string
	Number    int
	Title     string
	Status    string
	Repo      *string
	DependsOn []string
}

func treeRecords(issues []api.Issue, initiatives []api.Initiative) map[string]treeRecord {
	byID := make(map[string]treeRecord, len(issues)+len(initiatives))
	for _, issue := range issues {
		deps := make([]string, 0, len(issue.DependsOn)+1)
		for _, dep := range issue.DependsOn {
			deps = append(deps, strconv.Itoa(dep.Number))
		}
		if len(issue.InitiativeWaitsOn) > 0 && issue.Initiative != nil {
			deps = append(deps, strconv.Itoa(issue.Initiative.Number))
		}
		id := strconv.Itoa(issue.Number)
		byID[id] = treeRecord{
			ID: id, UUID: issue.ID, Kind: api.RecordKindIssue, Number: issue.Number,
			Title: issue.Title, Status: issue.Status, Repo: issue.Repo, DependsOn: deps,
		}
	}
	for _, initiative := range initiatives {
		deps := make([]string, 0, len(initiative.DependsOn))
		for _, dep := range initiative.DependsOn {
			deps = append(deps, strconv.Itoa(dep.Number))
		}
		id := strconv.Itoa(initiative.Number)
		byID[id] = treeRecord{
			ID: id, UUID: initiative.ID, Kind: api.RecordKindInitiative, Number: initiative.Number,
			Title: initiative.Name, Status: initiative.Status, DependsOn: deps,
		}
	}
	return byID
}

// isClosed is a completed or canceled issue, or a completed or dropped
// initiative.
func (r treeRecord) isClosed() bool {
	if r.Kind == api.RecordKindInitiative {
		return slices.Contains(api.InitiativeStatusesClosed, r.Status)
	}
	return r.Status == api.IssueStatusCompleted || r.Status == api.IssueStatusCanceled
}

// resolveTreeRecord finds the record an argument names among those already
// read: by number, by id, or an initiative by name.
func resolveTreeRecord(byID map[string]treeRecord, ref string) (treeRecord, error) {
	if record, ok := byID[strings.TrimPrefix(ref, "#")]; ok {
		return record, nil
	}
	for _, record := range byID {
		if record.UUID == ref || (record.Kind == api.RecordKindInitiative && record.Title == ref) {
			return record, nil
		}
	}
	return treeRecord{}, fmt.Errorf("no issue or initiative %q — `slate search <words>` finds one by title", ref)
}

// treeMatches keeps a tree when any record in it answers both filters. An
// empty status asks for trees holding unclosed work, which an open initiative
// is. A named status and a repo are an issue's, so only an issue answers them.
func treeMatches(members []string, byID map[string]treeRecord, issueStatus string, repo *string) bool {
	statusOK, repoOK := issueStatus == api.IssueStatusAll, repo == nil
	for _, id := range members {
		record := byID[id]
		if !statusOK {
			if issueStatus == "" {
				statusOK = !record.isClosed()
			} else {
				statusOK = record.Kind == api.RecordKindIssue && record.Status == issueStatus
			}
		}
		if !repoOK && record.Kind == api.RecordKindIssue && strValue(record.Repo) == *repo {
			repoOK = true
		}
	}
	return statusOK && repoOK
}

func printTrees(out io.Writer, g *graph.Graph, byID map[string]treeRecord, trees []drawTree, invert bool, maxDepth int) {
	if len(trees) == 0 {
		_, _ = fmt.Fprintln(out, "No dependency trees.")
		return
	}
	for i, tree := range trees {
		if i > 0 {
			_, _ = fmt.Fprintln(out)
		}
		for _, row := range g.Rows(tree.roots, invert, maxDepth) {
			record := byID[row.ID]
			line := treePrefix(row) + summaryMark(record.Status) + " " + strconv.Itoa(record.Number) + " "
			if record.Kind == api.RecordKindInitiative {
				line += "initiative "
			}
			line += record.Title
			if row.Repeated {
				line += " (*)"
			}
			_, _ = fmt.Fprintln(out, line)
		}
	}
}

// treeDocument is the machine rendering: nodes and edges, never the drawing,
// so a consumer needing another shape does not parse box characters.
type treeDocument struct {
	Nodes []treeNode `json:"nodes"`
	Edges []treeEdge `json:"edges"`
	Roots []int      `json:"roots"`
}

type treeNode struct {
	ID     string  `json:"id"`
	Kind   string  `json:"kind"`
	Number int     `json:"number"`
	Title  string  `json:"title"`
	Status string  `json:"status"`
	Repo   *string `json:"repo"`
}

// treeEdge runs from the record to what it waits on, whichever way the
// drawing ran. Only Roots reflects --invert.
type treeEdge struct {
	Record    int `json:"record"`
	DependsOn int `json:"depends_on"`
}

func buildTreeJSON(g *graph.Graph, byID map[string]treeRecord, trees []drawTree, invert bool, maxDepth int) treeDocument {
	doc := treeDocument{Nodes: []treeNode{}, Edges: []treeEdge{}, Roots: []int{}}
	for _, tree := range trees {
		drawn := make(map[string]bool)
		for _, row := range g.Rows(tree.roots, invert, maxDepth) {
			drawn[row.ID] = true
		}
		for _, id := range tree.members {
			if !drawn[id] {
				continue
			}
			record := byID[id]
			doc.Nodes = append(doc.Nodes, treeNode{
				ID: record.UUID, Kind: record.Kind, Number: record.Number, Title: record.Title, Status: record.Status, Repo: record.Repo,
			})
		}
		for _, edge := range g.Edges(tree.members) {
			if drawn[edge[0]] && drawn[edge[1]] {
				doc.Edges = append(doc.Edges, treeEdge{Record: byID[edge[0]].Number, DependsOn: byID[edge[1]].Number})
			}
		}
		for _, root := range tree.roots {
			doc.Roots = append(doc.Roots, byID[root].Number)
		}
	}
	return doc
}
