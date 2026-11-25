package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/gotermboard"
	"github.com/datapointchris/gotermstyle"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/datapointchris/slate/cli/internal/api"
	"github.com/datapointchris/slate/cli/internal/config"
)

// The size --once draws at where neither the environment nor a terminal gives one.
const (
	onceFallbackWidth  = 188
	onceFallbackHeight = 88
)

const tabInitiatives = "initiatives"

// tuiTab is one tab: its name, its board in the live program, and its print for --once and --json.
type tuiTab struct {
	name  string
	board func(in tabInputs) gotermboard.Board
	print func(cmd *cobra.Command, asJSON bool, in tabInputs) error
}

// tabInputs is what the tabs' boards are built from. The initiatives board is built only where
// its tab is printed or opened.
type tabInputs struct {
	client      *api.Client
	initiatives func() *initiativesBoard
}

// tuiTabs is every tab, in the order Tab cycles them.
func tuiTabs() []tuiTab {
	return []tuiTab{
		{
			name:  tabInitiatives,
			board: func(in tabInputs) gotermboard.Board { return in.initiatives() },
			print: func(cmd *cobra.Command, asJSON bool, in tabInputs) error {
				if err := readAndPrintTab(cmd, asJSON, in.initiatives()); err != nil {
					return err
				}
				if !cmd.Flags().Changed("status") {
					noteHiddenByStatus(cmd, "Someday, completed and dropped initiatives")
				}
				return nil
			},
		},
		{
			name:  tabRepos,
			board: func(in tabInputs) gotermboard.Board { return newReposBoard(in.client) },
			print: func(cmd *cobra.Command, asJSON bool, in tabInputs) error {
				return readAndPrintTab(cmd, asJSON, newReposBoard(in.client))
			},
		},
		{
			name:  tabIdeas,
			board: func(in tabInputs) gotermboard.Board { return newIdeasBoard(in.client) },
			print: func(cmd *cobra.Command, asJSON bool, in tabInputs) error {
				return readAndPrintTab(cmd, asJSON, newIdeasBoard(in.client))
			},
		},
		{
			name:  tabDocuments,
			board: func(in tabInputs) gotermboard.Board { return newDocumentsBoard(in.client) },
			print: func(cmd *cobra.Command, asJSON bool, in tabInputs) error {
				return readAndPrintTab(cmd, asJSON, newDocumentsBoard(in.client))
			},
		},
		{
			name:  tabPursuits,
			board: func(in tabInputs) gotermboard.Board { return newPursuitsBoard(in.client) },
			print: func(cmd *cobra.Command, asJSON bool, in tabInputs) error {
				return readAndPrintTab(cmd, asJSON, newPursuitsBoard(in.client))
			},
		},
		{
			name:  tabWorklogs,
			board: func(in tabInputs) gotermboard.Board { return newWorklogsBoard(in.client) },
			print: func(cmd *cobra.Command, asJSON bool, in tabInputs) error {
				return readAndPrintTab(cmd, asJSON, newWorklogsBoard(in.client))
			},
		},
	}
}

func tuiTabNames() []string {
	tabs := tuiTabs()
	names := make([]string, len(tabs))
	for i, tab := range tabs {
		names[i] = tab.name
	}
	return names
}

// onTerminal is whether a person holds the keyboard and sees the screen.
func onTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func newTUICommand() *cobra.Command {
	var (
		view, initiativeStatus, grouping string
		once, asJSON                     bool
		perTier, firstInList             int
	)
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "A live board of the initiatives, the repos, the ideas, the documents, the pursuits and the worklogs, in a program with a tab per view",
		Long: "One program with a tab per view, reading the API at its own pace. It draws its\n" +
			"last good read with that read's age, so a rollout leaves the board drawn. Tab\n" +
			"cycles the tabs once there is more than one.\n\n" +
			"initiatives is the total, each priority tier and each initiative on one set of\n" +
			"columns, an initiative's number and name above them: done over done and open, the\n" +
			"issues completed in 24 hours, 7 days and 30 days, the age of the last close, the\n" +
			"issues running or waiting, blocked and ready,\n" +
			"then a bar with one cell per issue. Canceled issues stay off the bar. 1 shows the\n" +
			"first of each tier, 2 the first in list order, 3 all of them, and + and - change\n" +
			"the count. / searches past what the scope shows. enter unfolds an initiative's\n" +
			"issues, and an issue's sessions and pull requests from its worklogs. enter on a\n" +
			"pull request opens `prs tui` on it, and o opens it in the browser. d opens an\n" +
			"initiative's or an issue's description, full screen where it runs past the\n" +
			"screen. A claimed initiative or issue names its holder after its title. f adds\n" +
			"summaries, s cycles the sort within a tier between\n" +
			strings.Join(boardSortLabels(), ", ") + ", and v cycles the grouping between\n" +
			strings.Join(initiativeGroupings, ", ") + ". A pursuit's or a\n" +
			"tag's line counts the closes of every initiative in it, including one completed\n" +
			"inside the window, and names its active initiative touched longest ago. An\n" +
			"initiative in two draws under both. ? lists every key and every read.\n" +
			"From 160 columns a side panel follows the cursor, and p hides it. On an\n" +
			"initiative it draws its issues and its completed ones over its life, so scope\n" +
			"added under it shows.\n\n" +
			"Some keys write, each through what the matching slate verb runs. M moves an issue\n" +
			"to the initiative you name, P pins or unpins the row, and < and > move it ahead\n" +
			"of the row above or behind the row below in the queue. A write asks first where\n" +
			"the row it moves past is hidden, where the queue holds records between the two\n" +
			"rows the screen does not draw there, or where another row's description is open.\n" +
			"The service scopes below cover the reads alone: M, < and > need\n" +
			"slate.records.plan, and P a person's login. C opens a Claude session on the row,\n" +
			"in the checkout the repo registry names for its repo: in a new window inside\n" +
			"tmux, and otherwise in this terminal until it ends.\n\n" +
			"repos is every repo the issues name on the same columns, then how long its oldest\n" +
			"ready issue has waited and its closes in each recent week. Repos with nothing\n" +
			"open and no close in those weeks fold under quiet. The panel draws a repo's\n" +
			"closes per week as columns and its ready issues by how long each has waited, and\n" +
			"on the total every repo's. s cycles the order.\n\n" +
			"ideas is every idea not promoted to an initiative, newest first, with its\n" +
			"priority, the day it was created, its pursuits, its tags and how many refs it\n" +
			"carries. u narrows it to one pursuit and # to one tag, each cycling back to all\n" +
			"after the last. enter opens an idea's read: its placements, its refs and its\n" +
			"description. tui-ideas.schema.json in slate's cli/internal/cli holds the shape\n" +
			"--json prints of it.\n\n" +
			fmt.Sprintf("documents is every document on the %d records written to last, each under its\n", documentRecordsStep) +
			fmt.Sprintf("record, the newest written first in both. + and - read %d records more or fewer.\n", documentRecordsStep) +
			"enter opens a document's latest version full screen, its line comments listed\n" +
			"after it, each quoting its line. No earlier version shows. / matches a document's\n" +
			"name, its kind and its record's title. tui-documents.schema.json in slate's\n" +
			"cli/internal/cli holds the shape --json prints of it.\n\n" +
			"pursuits is each pursuit's standing against the focus in force, as `slate focus\n" +
			"show` reads it: its share, its usage, its factor, its output, its initiatives and\n" +
			"ideas, and when it was last worked. The named shares come first, then the rest,\n" +
			"each the furthest under its share first. With no focus in force, one group holds\n" +
			"every pursuit, and SHARE and FACTOR read ·. The panel charts a pursuit's usage on\n" +
			"each day of the focus beside its share, as `slate focus usage` reads it, and the\n" +
			"total's stacks every named share and the rest. h and l move to the focus set\n" +
			"before and after, which show their shares and their figures at the end of their\n" +
			"last day. tui-pursuits.schema.json in slate's cli/internal/cli holds the shape\n" +
			"--json prints of it.\n\n" +
			"worklogs is the live worklogs, the expired ones whose claim ran out, and the\n" +
			fmt.Sprintf("newest %d ended, each with its step, stage, start, sessions, holder and what its\n", endedWorklogsStep) +
			fmt.Sprintf("claim waits on. + and - read %d ended more or fewer, up to %d. enter unfolds a\n", endedWorklogsStep, worklogsReadMost) +
			"worklog into its sessions and the commits, pull requests and landings it links.\n" +
			"enter on a pull request opens `prs tui` on it, and o opens a web link in the\n" +
			"browser and names any other. The panel holds the claim and its waits, each\n" +
			"session's span, the links and the document versions it wrote, then its tokens by\n" +
			"kind beside its record's total and the record's estimate, as `slate totals` reads\n" +
			"them. tui-worklogs.schema.json in slate's cli/internal/cli holds the shape --json\n" +
			"prints of it.\n\n" +
			"t on a row goes to another tab that draws what the row names, where one does, and\n" +
			"esc comes back. t on a pull request opens `prs tui` on it. * locks every tab to the\n" +
			"lines naming what the row names, such as one pursuit, and lifts the lock.\n\n" +
			"The next run opens on the tab last moved to, unless --view names one. Each tab\n" +
			"keeps its p panel, f summaries, v grouping, and the groups and rows folded and\n" +
			"opened, in $XDG_STATE_HOME/slate/tui-display.json. A run --grouping names keeps none.\n\n" +
			"--once prints one frame at COLUMNS by LINES, and --json the snapshot it is drawn\n" +
			"from, so an agent reads what a person reads. --grouping opens the initiatives tab\n" +
			"at a grouping, as v leaves it. With no terminal and neither flag it refuses.",
		Example: "  slate tui\n" +
			"  slate tui --view repos\n" +
			"  slate tui --per-tier 3\n" +
			"  slate tui --status someday\n" +
			"  COLUMNS=188 LINES=88 slate tui --once > frame.txt\n" +
			"  slate tui --grouping pursuit --once\n" +
			"  slate tui --view repos --json | jq '.board.repos[] | {repo, ready: (.ready | length)}'\n" +
			"  slate tui --view ideas --json | jq '.rows[] | {number, title, ref_count}'\n" +
			"  slate tui --view documents --json | jq '.records[] | {number: .record.number, documents: [.documents[].name]}'\n" +
			"  slate tui --view pursuits --json | jq '.rows[] | {name, share, usage, factor}'\n" +
			"  slate tui --view worklogs --json | jq '.live[] | {name, step, waiting: .claim.waiting.waiting_on}'\n" +
			"  slate tui --json | jq '.board.totals.states'\n" +
			"  slate tui --json | jq '.board.pursuits[] | {name: .pursuit.name, closes_30d: .closes.last_30d, untouched: .untouched_longest.name}'",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := gotermboard.RefuseUnknownView(view, tuiTabNames()); err != nil {
				return goclikit.UsageError(err)
			}
			if grouping != "" && !slices.Contains(initiativeGroupings, grouping) {
				return goclikit.UsageError(fmt.Errorf("--grouping takes one of %s, not %q", strings.Join(initiativeGroupings, ", "), grouping))
			}
			if cmd.Flags().Changed("status") && !slices.Contains(api.InitiativeStatuses, initiativeStatus) {
				return goclikit.UsageError(fmt.Errorf("unknown status %q — one of: %s", initiativeStatus, strings.Join(api.InitiativeStatuses, ", ")))
			}
			if once && asJSON {
				return goclikit.UsageError(errors.New("--once prints a frame and --json a snapshot, so give one of them"))
			}
			if cmd.Flags().Changed("per-tier") && cmd.Flags().Changed("limit") {
				return goclikit.UsageError(errors.New("--per-tier and --limit each set the scope, so give one of them"))
			}
			printing := once || asJSON
			if !printing && !onTerminal() {
				return goclikit.UsageError(errors.New("slate tui needs a terminal; --once prints one frame and --json the snapshot"))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			scope := boardScope{perTier: perTier, first: firstInList}
			if cmd.Flags().Changed("limit") {
				scope.kind = scopeFirstInList
			}
			in := tabInputs{client: client, initiatives: func() *initiativesBoard {
				return newInitiativesBoard(client, initiativeStatus, scope, grouping)
			}}
			if printing {
				return printOpeningTab(cmd, view, asJSON, in)
			}
			tabs, err := openTabs(tuiTabList(in), view)
			if err != nil {
				return err
			}
			// A remembered grouping would override the one --grouping names, so that run keeps none.
			if path := config.StatePath("tui-display.json"); path != "" && grouping == "" {
				tabs.RemembersDisplayIn(path)
			}
			_, err = tea.NewProgram(tabs).Run()
			return err
		},
	}
	cmd.Flags().StringVar(&view, "view", "", "Open on this tab: "+strings.Join(tuiTabNames(), ", "))
	withFlagValues(cmd, "view", tuiTabNames()...)
	cmd.Flags().StringVar(&grouping, "grouping", "", "Open the initiatives tab grouped by: "+strings.Join(initiativeGroupings, ", ")+" (default tier)")
	withFlagValues(cmd, "grouping", initiativeGroupings...)
	cmd.Flags().BoolVar(&once, "once", false, "Print one frame of the tab at COLUMNS by LINES, then exit")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the tab's snapshot as JSON to stdout")
	perTier = defaultBoardPerTier
	cmd.Flags().Var(limitValue{target: &perTier}, "per-tier", "Open on the first N of each priority tier")
	addCapFlag(cmd, &firstInList, defaultBoardFirst, "Open on the first N initiatives in list order")
	cmd.Flags().StringVar(&initiativeStatus, "status", "", "One of: "+strings.Join(api.InitiativeStatuses, ", ")+" (default active)")
	withFlagValues(cmd, "status", api.InitiativeStatuses...)
	return cmd
}

// tuiTabList is every tab with its live board.
func tuiTabList(in tabInputs) []gotermboard.Tab {
	tabs := tuiTabs()
	list := make([]gotermboard.Tab, len(tabs))
	for i, tab := range tabs {
		list[i] = gotermboard.Tab{Name: tab.name, Board: tab.board(in)}
	}
	return list
}

// openTabs is the program of tabs, opening on view, where t on a pull request opens prs.
func openTabs(tabs []gotermboard.Tab, view string) (*gotermboard.Tabs, error) {
	program, err := gotermboard.NewTabs(tabs, gotermstyle.PaletteFor(os.Stdout), view)
	if err != nil {
		return nil, err
	}
	program.OpensOutside(prsOnPullRequest)
	return program, nil
}

// printOpeningTab reads the one tab --view names, the first where it names none, and builds no
// other.
func printOpeningTab(cmd *cobra.Command, view string, asJSON bool, in tabInputs) error {
	tabs := tuiTabs()
	at := slices.IndexFunc(tabs, func(tab tuiTab) bool { return tab.name == view })
	return tabs[max(at, 0)].print(cmd, asJSON, in)
}

// printableTab is a tab's board that --once and --json read once and print.
type printableTab[S any] interface {
	gotermboard.SnapshotBoard[S]
	readOnce(ctx context.Context) error
}

// readAndPrintTab reads board once, then prints its frame, or its snapshot where asJSON is set.
func readAndPrintTab[S any](cmd *cobra.Command, asJSON bool, board printableTab[S]) error {
	if err := board.readOnce(cmd.Context()); err != nil {
		return handleAPIError(err)
	}
	return gotermboard.PrintTab(cmd.OutOrStdout(), asJSON, board, gotermstyle.PaletteFor(os.Stdout), onceFallbackWidth, onceFallbackHeight)
}
