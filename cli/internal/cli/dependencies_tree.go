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
		Use:   "tree [<issue>]",
		Short: "Draw the dependency trees, or the one an issue sits in",
		Long: "Name an issue to draw the tree it belongs to, or nothing to draw every tree.\n" +
			"An issue with no dependency either way is in no tree.\n" +
			"\n" +
			"A row is something the row it hangs from waits on. --invert reads the edges\n" +
			"the other way, so a row waits on the row it hangs from.\n" +
			"\n" +
			"--status and --repo choose which trees are drawn, never which rows. A tree is\n" +
			"kept when any of its issues matches, and then drawn whole. Dropping a row\n" +
			"would orphan everything below it. Trees with nothing unclosed are hidden by\n" +
			"default.\n" +
			"\n" +
			"(*) marks an issue whose dependencies were drawn higher up.",
		Example: "  slate dependencies tree\n" +
			"  slate dependencies tree 412\n" +
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
			// Every issue, whatever the flags: a closed issue mid-chain is what
			// joins the halves either side of it.
			issues, err := client.ListIssues(cmd.Context(), api.IssueFilter{Status: api.IssueStatusAll}, api.Window{}, issueZone(), nil)
			if err != nil {
				return handleAPIError(err)
			}
			byID := make(map[string]api.Issue, len(issues))
			nodes := make([]graph.Node, 0, len(issues))
			for _, issue := range issues {
				byID[issue.ID] = issue
				deps := make([]string, 0, len(issue.DependsOn))
				for _, dep := range issue.DependsOn {
					deps = append(deps, dep.ID)
				}
				nodes = append(nodes, graph.Node{ID: issue.ID, Order: issue.Number, Deps: deps})
			}
			g := graph.Build(nodes)

			var trees []drawTree
			if len(args) == 1 {
				issue, err := resolveFetchedIssue(issues, args[0])
				if err != nil {
					return err
				}
				tree, connected := treeForItem(g, issue.ID)
				if !connected {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "#%d has no dependencies either way.\n", issue.Number)
				}
				trees = []drawTree{tree}
			} else {
				for _, members := range g.Components() {
					if issueComponentMatches(members, byID, issueStatus, filter) {
						trees = append(trees, drawTree{roots: g.Roots(members, invert), members: members})
					}
				}
			}

			if len(args) == 0 && !cmd.Flags().Changed("status") {
				defer noteHiddenByStatus(cmd, "Trees with nothing unclosed")
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), buildIssueTreeJSON(g, byID, trees, invert, maxDepth))
			}
			printIssueTrees(cmd.OutOrStdout(), g, byID, trees, invert, maxDepth)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the nodes and edges as JSON to stdout")
	cmd.Flags().BoolVar(&invert, "invert", false, "Draw what each issue releases instead of what it waits on")
	cmd.Flags().IntVar(&depth, "depth", 0, "Stop drawing below this many levels (default every level)")
	cmd.Flags().StringVar(&repo, "repo", "", "Only trees holding an issue on this repo (empty string for issues on no repo)")
	cmd.Flags().StringVar(&issueStatus, "status", "", "Only trees holding an issue in this state: "+strings.Join(api.IssueStatuses, ", ")+" (default unclosed)")
	withFlagValues(cmd, "status", api.IssueStatuses...)
	return cmd
}

// resolveFetchedIssue finds the issue an argument names among those already
// read, by number or by id.
func resolveFetchedIssue(issues []api.Issue, ref string) (api.Issue, error) {
	number, numberErr := strconv.Atoi(strings.TrimPrefix(ref, "#"))
	for _, issue := range issues {
		if (numberErr == nil && issue.Number == number) || issue.ID == ref {
			return issue, nil
		}
	}
	return api.Issue{}, fmt.Errorf("no issue %q — `slate search <words>` finds one by title", ref)
}

// issueComponentMatches keeps a tree when any issue in it answers both filters.
// An empty status asks for trees holding unclosed work.
func issueComponentMatches(members []string, byID map[string]api.Issue, issueStatus string, repo *string) bool {
	statusOK, repoOK := issueStatus == api.IssueStatusAll, repo == nil
	for _, id := range members {
		issue := byID[id]
		if !statusOK {
			if issueStatus == "" {
				statusOK = !isClosedIssue(issue)
			} else {
				statusOK = issue.Status == issueStatus
			}
		}
		if !repoOK && strValue(issue.Repo) == *repo {
			repoOK = true
		}
	}
	return statusOK && repoOK
}

func printIssueTrees(out io.Writer, g *graph.Graph, byID map[string]api.Issue, trees []drawTree, invert bool, maxDepth int) {
	if len(trees) == 0 {
		_, _ = fmt.Fprintln(out, "No dependency trees.")
		return
	}
	for i, tree := range trees {
		if i > 0 {
			_, _ = fmt.Fprintln(out)
		}
		for _, row := range g.Rows(tree.roots, invert, maxDepth) {
			issue := byID[row.ID]
			line := treePrefix(row) + summaryMark(issue.Status) + " " + strconv.Itoa(issue.Number) + " " + issue.Title
			if row.Repeated {
				line += " (*)"
			}
			_, _ = fmt.Fprintln(out, line)
		}
	}
}

// issueTreeDocument is the machine rendering: nodes and edges, never the
// drawing, so a consumer needing another shape does not parse box characters.
type issueTreeDocument struct {
	Nodes []issueTreeNode `json:"nodes"`
	Edges []issueTreeEdge `json:"edges"`
	Roots []int           `json:"roots"`
}

type issueTreeNode struct {
	ID     string  `json:"id"`
	Number int     `json:"number"`
	Title  string  `json:"title"`
	Status string  `json:"status"`
	Repo   *string `json:"repo"`
}

// issueTreeEdge runs from the issue to what it waits on, whichever way the
// drawing ran. Only Roots reflects --invert.
type issueTreeEdge struct {
	Issue     int `json:"issue"`
	DependsOn int `json:"depends_on"`
}

func buildIssueTreeJSON(g *graph.Graph, byID map[string]api.Issue, trees []drawTree, invert bool, maxDepth int) issueTreeDocument {
	doc := issueTreeDocument{Nodes: []issueTreeNode{}, Edges: []issueTreeEdge{}, Roots: []int{}}
	for _, tree := range trees {
		drawn := make(map[string]bool)
		for _, row := range g.Rows(tree.roots, invert, maxDepth) {
			drawn[row.ID] = true
		}
		for _, id := range tree.members {
			if !drawn[id] {
				continue
			}
			issue := byID[id]
			doc.Nodes = append(doc.Nodes, issueTreeNode{
				ID: issue.ID, Number: issue.Number, Title: issue.Title, Status: issue.Status, Repo: issue.Repo,
			})
		}
		for _, edge := range g.Edges(tree.members) {
			if drawn[edge[0]] && drawn[edge[1]] {
				doc.Edges = append(doc.Edges, issueTreeEdge{Issue: byID[edge[0]].Number, DependsOn: byID[edge[1]].Number})
			}
		}
		for _, root := range tree.roots {
			doc.Roots = append(doc.Roots, byID[root].Number)
		}
	}
	return doc
}
