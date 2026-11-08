package cli

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/gotermstyle"
	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

// boardBar is an initiative's issues as every bar slate draws them: the done ones, then
// the open ones as openKinds splits them. A canceled issue is in none.
type boardBar struct {
	Done     int `json:"done"`
	UnderWay int `json:"under_way"`
	Blocked  int `json:"blocked"`
	Rest     int `json:"rest"`
}

func barOf(counts api.BoardCounts) boardBar {
	underWay, blocked, rest := openKinds(counts.States, counts.OpenCount)
	return boardBar{Done: counts.States.Completed, UnderWay: underWay, Blocked: blocked, Rest: rest}
}

// boardInitiative is the board read's row with its bar beside its counts.
type boardInitiative struct {
	api.BoardInitiative
	Bar boardBar `json:"bar"`
}

// printedBoard is the board read as --json prints it, each initiative carrying its bar.
type printedBoard struct {
	api.InitiativeBoard
	Initiatives []boardInitiative `json:"initiatives"`
}

func newInitiativesBoardCommand() *cobra.Command {
	var (
		initiativeStatus string
		asJSON           bool
	)
	cmd := &cobra.Command{
		Use:   "board",
		Short: "Show each initiative's bar of done, under way, blocked and the rest, as slate tui draws it",
		Long: "The initiatives at --status, active by default, in list order. Each has its bar:\n" +
			"the done issues, then the open ones split into under way, blocked and the rest,\n" +
			"as every bar slate draws splits them. An issue under a live claim counts as\n" +
			"running, or as waiting where its holder parked it, whatever else holds it back,\n" +
			"and both count as under way.\n\n" +
			"--json prints the board read whole, as slate tui reads it: each initiative's\n" +
			"issues counted by state, its closes and its issues, with its bar under `bar`.\n" +
			"A reader draws that bar rather than splitting the states again.",
		Example:     "  slate initiatives board\n  slate initiatives board --status all --json",
		Annotations: scopesForService(scopeRecordsRead),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("status") && !slices.Contains(api.InitiativeStatuses, initiativeStatus) {
				return goclikit.UsageError(fmt.Errorf("unknown status %q — one of: %s", initiativeStatus, strings.Join(api.InitiativeStatuses, ", ")))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			board, err := client.ReadInitiativeBoard(cmd.Context(), initiativeStatus, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			printed := printedBoard{InitiativeBoard: board, Initiatives: make([]boardInitiative, 0, len(board.Initiatives))}
			for _, initiative := range board.Initiatives {
				printed.Initiatives = append(printed.Initiatives, boardInitiative{BoardInitiative: initiative, Bar: barOf(initiative.BoardCounts)})
			}
			if !cmd.Flags().Changed("status") {
				defer noteHiddenByStatus(cmd, "Someday, completed and dropped initiatives")
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), printed)
			}
			printInitiativesBoard(cmd.OutOrStdout(), printed.Initiatives)
			return nil
		},
	}
	cmd.Flags().StringVar(&initiativeStatus, "status", "", "One of: "+strings.Join(api.InitiativeStatuses, ", ")+" (default active)")
	withFlagValues(cmd, "status", api.InitiativeStatuses...)
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the board read, with each initiative's bar, as JSON to stdout")
	return goclikit.WithRecoveryHints(cmd, initiativeHints...)
}

// printInitiativesBoard prints a row an initiative: its number, progress's bar, its done
// over its done and open, its open work under way and blocked, and its name.
func printInitiativesBoard(out io.Writer, initiatives []boardInitiative) {
	if len(initiatives) == 0 {
		_, _ = fmt.Fprintln(out, "No initiatives.")
		return
	}
	p, clip := gotermstyle.PaletteFor(out), clipsToTerminal(out)
	numbers, fractions, open := make([]string, len(initiatives)), make([]string, len(initiatives)), make([]string, len(initiatives))
	for i, initiative := range initiatives {
		bar := initiative.Bar
		numbers[i] = strconv.Itoa(initiative.Number)
		fractions[i] = fmt.Sprintf("%d/%d", bar.Done, bar.Done+initiative.OpenCount)
		open[i] = fmt.Sprintf("%d under way · %d blocked", bar.UnderWay, bar.Blocked)
	}
	numberWidth, fractionWidth, openWidth := widest(numbers), widest(fractions), widest(open)
	used := 2 + numberWidth + 2 + progressBarCells + 2 + fractionWidth + 2 + openWidth + 2
	for i, initiative := range initiatives {
		name := initiative.Name
		if clip {
			name = gotermstyle.Clip(name, used)
		}
		cells := []string{
			"", paint(p.Cyan, numbers[i], numberWidth),
			progressBar(p, initiative.States, initiative.Bar.Done, initiative.OpenCount),
			padRight(fractions[i], fractionWidth), padRight(open[i], openWidth), name,
		}
		_, _ = fmt.Fprintln(out, strings.Join(cells, "  "))
	}
}
