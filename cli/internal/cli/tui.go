package cli

import (
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
)

// The size --once draws at where neither the environment nor a terminal gives one.
const (
	onceFallbackWidth  = 188
	onceFallbackHeight = 88
)

// The tabs, in the order Tab cycles them.
const tabInitiatives = "initiatives"

func tuiTabs() []string { return []string{tabInitiatives} }

func newTUICommand() *cobra.Command {
	var (
		view, initiativeStatus string
		once, asJSON           bool
		perTier, firstInList   int
	)
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "A live board of the initiatives, in a program with a tab per view",
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
			"issues and opens an issue's description, d opens an initiative's description, f\n" +
			"adds summaries, and s cycles the sort within a tier. ? lists every key and every\n" +
			"read. From 160 columns a side panel follows the cursor, and p hides it.\n\n" +
			"--once prints one frame at COLUMNS by LINES, and --json the snapshot it is drawn\n" +
			"from, so an agent reads what a person reads. With no terminal and neither flag it\n" +
			"refuses.",
		Example: "  slate tui\n" +
			"  slate tui --per-tier 3\n" +
			"  slate tui --status someday\n" +
			"  COLUMNS=188 LINES=88 slate tui --once > frame.txt\n" +
			"  slate tui --json | jq '.board.totals.states'",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if view != "" && !slices.Contains(tuiTabs(), view) {
				return goclikit.UsageError(fmt.Errorf("--view takes one of %s, not %q", strings.Join(tuiTabs(), ", "), view))
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
			if !printing && (!term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd()))) {
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
			board := newInitiativesBoard(client, initiativeStatus, scope)
			if !printing {
				tabs, err := gotermboard.NewTabs([]gotermboard.Tab{{Name: tabInitiatives, Board: board}}, gotermstyle.PaletteFor(os.Stdout), view)
				if err != nil {
					return err
				}
				_, err = tea.NewProgram(tabs).Run()
				return err
			}
			if err := board.readOnce(cmd.Context()); err != nil {
				return handleAPIError(err)
			}
			if !cmd.Flags().Changed("status") {
				defer noteHiddenByStatus(cmd, "Someday, completed and dropped initiatives")
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), board.snapshot())
			}
			width, height := gotermboard.OnceSize(onceFallbackWidth, onceFallbackHeight)
			_, err = fmt.Fprintln(cmd.OutOrStdout(), gotermboard.Once(board, gotermstyle.PaletteFor(os.Stdout), width, height))
			return err
		},
	}
	cmd.Flags().StringVar(&view, "view", "", "Open on this tab: "+strings.Join(tuiTabs(), ", "))
	withFlagValues(cmd, "view", tuiTabs()...)
	cmd.Flags().BoolVar(&once, "once", false, "Print one frame of the tab at COLUMNS by LINES, then exit")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the tab's snapshot as JSON to stdout")
	perTier = defaultBoardPerTier
	cmd.Flags().Var(limitValue{target: &perTier}, "per-tier", "Open on the first N of each priority tier")
	addCapFlag(cmd, &firstInList, defaultBoardFirst, "Open on the first N initiatives in list order")
	cmd.Flags().StringVar(&initiativeStatus, "status", "", "One of: "+strings.Join(api.InitiativeStatuses, ", ")+" (default active)")
	withFlagValues(cmd, "status", api.InitiativeStatuses...)
	return cmd
}
