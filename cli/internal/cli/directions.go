package cli

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/gotermstyle"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/datapointchris/slate/cli/internal/api"
)

var directionHints = []string{
	anyRecordHint,
	"Directions that are not active are hidden: slate directions list --status all",
}

// The help each kind's --direction carries, on a create, an edit and a list.
const (
	directionSetHelp     = "Direction it moves in, by number or exact name (repeatable; see: slate directions list)"
	directionReplaceHelp = "Direction, by number or exact name (repeatable; replaces the set, empty removes every direction)"
	directionFilterHelp  = "Only records in this direction, by number or exact name, an issue through its initiative (see: slate directions list)"
)

// directionCreateStatuses are where a new direction may start.
var directionCreateStatuses = []string{"active", "someday"}

// directionNumbers resolves each --direction to its number. A number passes as
// it is, so the API refuses one naming no direction. A name is read by the API,
// which names the near ones where it matches none.
func directionNumbers(cmd *cobra.Command, client *api.Client, refs []string) ([]int, error) {
	numbers := make([]int, 0, len(refs))
	for _, ref := range refs {
		if digits := strings.TrimPrefix(ref, "#"); isItemNumber(digits) {
			number, err := strconv.Atoi(digits)
			if err != nil {
				return nil, goclikit.UsageError(fmt.Errorf("--direction %q is not a number the API can read: %w", ref, err))
			}
			numbers = append(numbers, number)
			continue
		}
		direction, err := client.GetDirection(cmd.Context(), ref)
		if err != nil {
			return nil, handleArgumentAPIError(err)
		}
		numbers = append(numbers, direction.Number)
	}
	return numbers, nil
}

// directionsEdited reads an edit's --direction as its numbers: nil where it was
// not passed, an empty set where it was passed empty, and the numbers otherwise.
func directionsEdited(cmd *cobra.Command, client *api.Client, f *pflag.FlagSet, refs []string) (*[]int, error) {
	switch readFlagIntent(f, "direction") {
	case flagClears:
		return &[]int{}, nil
	case flagCarriesValues:
		numbers, err := directionNumbers(cmd, client, refs)
		if err != nil {
			return nil, err
		}
		return &numbers, nil
	case flagMixesEmptyWithValues:
		return nil, goclikit.UsageError(errors.New("--direction given both a value and an empty one — an empty value removes every direction, so pass one or the other"))
	case flagUntouched:
	}
	return nil, nil
}

// An API from before directions drops --direction from a request and answers
// as though it held it. Each check below refuses such an answer by the
// directions it carries.

// inDirection says whether directions holds the one ref names, by number or
// by exact name.
func inDirection(ref string, directions []api.DirectionSummary) bool {
	digits := strings.TrimPrefix(ref, "#")
	return slices.ContainsFunc(directions, func(direction api.DirectionSummary) bool {
		if isItemNumber(digits) {
			return strconv.Itoa(direction.Number) == digits
		}
		return direction.Name == ref
	})
}

// directionNamed is a --direction as an error names it: #N for a number, and
// the quoted name otherwise.
func directionNamed(ref string) string {
	if digits := strings.TrimPrefix(ref, "#"); isItemNumber(digits) {
		return "#" + digits
	}
	return strconv.Quote(ref)
}

// honoredDirectionWrite refuses a write whose answer is in other directions
// than the --direction it was sent. A nil want sent none.
func honoredDirectionWrite(want *[]int, number int, carried []api.DirectionSummary) error {
	if want == nil {
		return nil
	}
	got := make([]int, 0, len(carried))
	for _, direction := range carried {
		got = append(got, direction.Number)
	}
	wanted := slices.Clone(*want)
	slices.Sort(wanted)
	if slices.Equal(slices.Compact(wanted), got) {
		return nil
	}
	return fmt.Errorf("the API ignored --direction: #%d was written in %s rather than %s, so it predates directions",
		number, directionNumbersOrNone(got), directionNumbersOrNone(wanted))
}

// directionList is each direction by number and name, as a detail line reads it.
func directionList(directions []api.DirectionSummary) string {
	shown := make([]string, 0, len(directions))
	for _, direction := range directions {
		shown = append(shown, fmt.Sprintf("#%d %s", direction.Number, direction.Name))
	}
	return strings.Join(shown, ", ")
}

func directionNumbersOrNone(numbers []int) string {
	if len(numbers) == 0 {
		return "no direction"
	}
	shown := make([]string, 0, len(numbers))
	for _, number := range numbers {
		shown = append(shown, "#"+strconv.Itoa(number))
	}
	return strings.Join(shown, ", ")
}

func newDirectionsCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "directions",
		Short: "Name the few ways the work moves toward its vision, and the ideas and initiatives in each",
		Long: "A direction is one way the work moves toward its vision, and it ends when the\n" +
			"work gets there. Ideas and initiatives join directions with --direction, and an\n" +
			"issue reads its initiative's. A direction holds no work, so `slate next` never\n" +
			"lists one and no claim holds one, and it lends no priority. There are only so\n" +
			"many ways to go at once, so `slate directions list` counts the active ones.",
		Example: "  slate directions list                                         the active ones, with what is in each\n" +
			"  slate directions create --name \"Data Warehouse\" --summary \"All data flows through the warehouse\"\n" +
			"  slate initiatives edit 2653 --direction \"Data Warehouse\"\n" +
			"  slate issues list --direction \"Data Warehouse\"",
	})
	goclikit.WithRecoveryHints(cmd, directionHints...)
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupReading,
		newDirectionsListCommand(),
		newDirectionsShowCommand(),
	)...)
	cmd.AddCommand(inGroup(groupChanging,
		newDirectionsCreateCommand(),
		newDirectionsEditCommand(),
		newDirectionsCompleteCommand(),
		newDirectionsDropCommand(),
		newDirectionsShelveCommand(),
		newDirectionsReopenCommand(),
	)...)
	return cmd
}

func newDirectionsListCommand() *cobra.Command {
	var (
		asJSON          bool
		directionStatus string
		limit           int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the directions with the ideas and initiatives in each, active first",
		Long: "The header counts the active directions. INITIATIVES and IDEAS count every one\n" +
			"in the direction, whatever its status.",
		Example:     "  slate directions list\n  slate directions list --status all --json",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("status") && !slices.Contains(api.DirectionStatuses, directionStatus) {
				return goclikit.UsageError(fmt.Errorf("unknown status %q — one of: %s", directionStatus, strings.Join(api.DirectionStatuses, ", ")))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			directions, err := client.ListDirections(cmd.Context(), directionStatus, limitFlag(cmd))
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), directions)
			}
			if !cmd.Flags().Changed("status") {
				defer noteHiddenByStatus(cmd, "Directions that are not active")
			}
			printDirections(cmd.OutOrStdout(), directions)
			return nil
		},
	}
	cmd.Flags().StringVar(&directionStatus, "status", "", "One of: "+strings.Join(api.DirectionStatuses, ", ")+" (default active)")
	withFlagValues(cmd, "status", api.DirectionStatuses...)
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output directions as JSON to stdout")
	addLimitFlag(cmd, &limit)
	return cmd
}

// directionShown is a direction, each initiative in it as the initiatives list reads
// it, with its counts and checked steps, and the changes its show read.
type directionShown struct {
	api.DirectionDetail
	InitiativeCounts []api.Initiative `json:"initiative_counts"`
	Changes          *recordChanges   `json:"changes,omitempty"`
}

// readDirectionShown reads each initiative in the direction, whatever its status, for
// its counts and its checked steps.
func readDirectionShown(cmd *cobra.Command, client *api.Client, direction api.DirectionDetail) (directionShown, error) {
	shown := directionShown{DirectionDetail: direction, InitiativeCounts: []api.Initiative{}}
	if len(direction.Initiatives) == 0 {
		return shown, nil
	}
	filter := api.InitiativeFilter{Status: "all", Direction: strconv.Itoa(direction.Number)}
	initiatives, err := client.ListInitiatives(cmd.Context(), filter, nil)
	if err != nil {
		return directionShown{}, handleAPIError(err)
	}
	shown.InitiativeCounts = initiatives
	return shown, nil
}

func newDirectionsShowCommand() *cobra.Command {
	var (
		asJSON      bool
		changeCount int
	)
	cmd := &cobra.Command{
		Use:   "show <direction>",
		Short: "Show a direction, each idea and initiative in it, and its changes",
		Long: "A direction is named by its number or its exact name. Each initiative in it, whatever\n" +
			"its status, prints with its open, done and canceled issues and the steps checked on it.\n" +
			"Each idea prints by number and name.",
		Example:     "  slate directions show \"Data Warehouse\"\n  slate directions show 2950 --json",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			direction, err := client.GetDirection(cmd.Context(), args[0])
			if err != nil {
				return handleArgumentAPIError(err)
			}
			shown, err := readDirectionShown(cmd, client, direction)
			if err != nil {
				return err
			}
			if shown.Changes, err = readChanges(cmd, client, direction.Number, changeCount, asJSON); err != nil {
				return err
			}
			defer noteChanges(cmd, shown.Changes)
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), shown)
			}
			printDirectionShown(cmd.OutOrStdout(), shown, time.Now())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the direction as JSON to stdout")
	addChangesFlag(cmd, &changeCount)
	return cmd
}

func newDirectionsCreateCommand() *cobra.Command {
	var (
		name, summary, description, directionStatus string
		asJSON                                      bool
	)
	cmd := &cobra.Command{
		Use:   "create --name <name> --summary <where it ends> [flags]",
		Short: "Name a direction the work moves in",
		Long: "--summary says where the direction ends, on one line, and is required. An\n" +
			"active direction's name is its own, so a second active one by that name is\n" +
			"refused. --status someday names it without taking it up, and `slate directions\n" +
			"reopen` takes it up later.",
		Example:     "  slate directions create --name \"Data Warehouse\" --summary \"All data flows through the warehouse, and every tool queries its tables\"",
		Args:        cobra.NoArgs,
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(name) == "" {
				return goclikit.UsageError(errors.New("--name is required"))
			}
			if strings.TrimSpace(summary) == "" {
				return goclikit.UsageError(errors.New("--summary is required — say where the direction ends, on one line"))
			}
			f := cmd.Flags()
			if f.Changed("status") && !slices.Contains(directionCreateStatuses, directionStatus) {
				return goclikit.UsageError(fmt.Errorf("unknown status %q — one of: %s", directionStatus, strings.Join(directionCreateStatuses, ", ")))
			}
			in := api.DirectionCreateInput{
				Name: name, Summary: summary, Description: valuedStrFlag(f, "description", &description), Status: nonEmpty(directionStatus),
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.CreateDirection(cmd.Context(), in)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, directionLine("Named", written.Direction))
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Direction name, unique among active ones (required)")
	cmd.Flags().StringVar(&summary, "summary", "", "Where the direction ends, on one line (required)")
	cmd.Flags().StringVar(&description, "description", "", "The direction in full")
	cmd.Flags().StringVar(&directionStatus, "status", "", "One of: "+strings.Join(directionCreateStatuses, ", ")+" (default active)")
	withFlagValues(cmd, "status", directionCreateStatuses...)
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newDirectionsEditCommand() *cobra.Command {
	var (
		name, summary, description string
		asJSON                     bool
	)
	cmd := &cobra.Command{
		Use:   "edit <direction> [flags]",
		Short: "Rename a direction, or change its summary or description",
		Long: "Update only the fields whose flags you pass. --description \"\" clears it.\n" +
			"Status moves through complete, drop, shelve and reopen.",
		Example: "  slate directions edit \"Data Warehouse\" --name \"Warehouse\"\n" +
			"  slate directions edit 2950 --summary \"Every tool reads the warehouse\"",
		SuggestFor:  editMistakenFor,
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			in := api.DirectionUpdateInput{
				Name:        valuedStrFlag(f, "name", &name),
				Summary:     valuedStrFlag(f, "summary", &summary),
				Description: valuedStrFlag(f, "description", &description),
			}
			var clear []string
			if readFlagIntent(f, "description") == flagClears {
				clear = append(clear, "description")
			}
			if in == (api.DirectionUpdateInput{}) && len(clear) == 0 {
				return goclikit.UsageError(errors.New("nothing to change — pass at least one of --name/--summary/--description"))
			}
			return runDirectionUpdate(cmd, args[0], in, clear, asJSON, "Updated")
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New name")
	cmd.Flags().StringVar(&summary, "summary", "", "Where the direction ends, on one line")
	cmd.Flags().StringVar(&description, "description", "", "New description (empty clears)")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newDirectionsShelveCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "shelve <direction>",
		Short: "Set a direction aside for someday, out of the active count",
		Long: "For a way to go that is named but not taken up. Every idea and initiative in\n" +
			"it stays in it, and it gives up its name, so a new direction may take it.\n" +
			"`slate directions reopen` takes it up again.",
		Example:     "  slate directions shelve \"Mobile App\"",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			someday := "someday"
			return runDirectionUpdate(cmd, args[0], api.DirectionUpdateInput{Status: &someday}, nil, asJSON, "Shelved")
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newDirectionsReopenCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:         "reopen <direction>",
		Short:       "Return a shelved or finished direction to the active list",
		Long:        "Refused if an active direction has taken the name in the meantime.",
		Example:     "  slate directions reopen \"Mobile App\"",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			active := "active"
			return runDirectionUpdate(cmd, args[0], api.DirectionUpdateInput{Status: &active}, nil, asJSON, "Reopened")
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newDirectionsCompleteCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:         "complete <direction>",
		Short:       "Mark a direction reached, which hides it",
		Long:        "Every idea and initiative in it stays in it, and keeps its own status.",
		Example:     "  slate directions complete \"Data Warehouse\"",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			completed := "completed"
			return runDirectionUpdate(cmd, args[0], api.DirectionUpdateInput{Status: &completed}, nil, asJSON, "Completed")
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newDirectionsDropCommand() *cobra.Command {
	var (
		reason string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:         "drop <direction> --reason <why>",
		Short:       "Close a direction the work will no longer move in",
		Long:        "--reason is required, so nobody takes it up again without reading why.",
		Example:     "  slate directions drop \"Mobile App\" --reason \"Folded into Web Client\"",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(reason) == "" {
				return goclikit.UsageError(errors.New("--reason is required — say why it is dropped"))
			}
			dropped := "dropped"
			return runDirectionUpdate(cmd, args[0], api.DirectionUpdateInput{Status: &dropped, StatusReason: &reason}, nil, asJSON, "Dropped")
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Why it is dropped (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func runDirectionUpdate(cmd *cobra.Command, ref string, in api.DirectionUpdateInput, clear []string, asJSON bool, verb string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return handleAPIError(err)
	}
	written, err := client.UpdateDirection(cmd.Context(), ref, in, clear)
	if err != nil {
		return handleArgumentAPIError(err)
	}
	return reportWrite(cmd, asJSON, written, written.WriteEffects, directionLine(verb, written.Direction))
}

func directionLine(verb string, direction api.Direction) string {
	return fmt.Sprintf("%s direction #%d %q (%s)", verb, direction.Number, direction.Name, direction.Status)
}

func printDirections(out io.Writer, directions []api.Direction) {
	active := 0
	for _, direction := range directions {
		if direction.Status == "active" {
			active++
		}
	}
	_, _ = fmt.Fprintf(out, "%d active %s\n\n", active, plural(active, "direction", "directions"))
	if len(directions) == 0 {
		_, _ = fmt.Fprintln(out, "No directions. Name one with `slate directions create --name ... --summary ...`.")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NUMBER\tSTATUS\tINITIATIVES\tIDEAS\tNAME\tSUMMARY")
	for _, direction := range directions {
		_, _ = fmt.Fprintf(tw, "#%d\t%s\t%d\t%d\t%s\t%s\n",
			direction.Number, direction.Status, len(direction.Initiatives), len(direction.Ideas), direction.Name, direction.Summary)
	}
	_ = tw.Flush()
}

// printDirectionShown is what every show of a direction prints.
func printDirectionShown(out io.Writer, shown directionShown, now time.Time) {
	printDirectionDetail(out, shown.Direction, shown.InitiativeCounts)
	printRecordParts(out, shown.RecordParts, now)
	printChanges(out, shown.Changes, now)
}

func printDirectionDetail(out io.Writer, direction api.Direction, counted []api.Initiative) {
	p := gotermstyle.PaletteFor(out)
	_, _ = fmt.Fprintf(out, "%s  %s\n", p.Cyan("#"+strconv.Itoa(direction.Number)), p.Bold(direction.Name))
	_, _ = fmt.Fprintf(out, "  summary:      %s\n", direction.Summary)
	status := direction.Status
	if direction.ClosedTS != nil {
		status += " (" + localDay(*direction.ClosedTS) + ")"
	}
	_, _ = fmt.Fprintf(out, "  status:       %s\n", status)
	if reason := strValue(direction.StatusReason); reason != "" {
		_, _ = fmt.Fprintf(out, "  reason:       %s\n", reason)
	}
	printMembers(out, "initiatives:", direction.Initiatives, initiativeCountsLines(p, counted))
	printMembers(out, "ideas:", direction.Ideas, nil)
	if description := strValue(direction.Description); description != "" {
		_, _ = fmt.Fprintf(out, "\nDescription:\n%s\n", indent(description, "  "))
	}
}

// printMembers prints each record in a direction a line under the label, with
// its status where it has one, or none under the label where it holds none.
// Where lines holds a record's number, its line prints in their place.
func printMembers(out io.Writer, label string, members []api.RecordSummary, lines map[int]string) {
	if len(members) == 0 {
		_, _ = fmt.Fprintf(out, "  %-13s none\n", label)
		return
	}
	for i, member := range members {
		if i == 0 {
			_, _ = fmt.Fprintf(out, "  %-13s ", label)
		} else {
			_, _ = fmt.Fprintf(out, "  %-13s ", "")
		}
		line, held := lines[member.Number]
		if !held {
			line = fmt.Sprintf("#%d %s", member.Number, member.Title)
			if status := strValue(member.Status); status != "" {
				line += " (" + status + ")"
			}
		}
		_, _ = fmt.Fprintln(out, line)
	}
}

// initiativeCountsLines is each initiative's line by its number: its name and
// status, its open, done and canceled issues, and each step checked on it.
func initiativeCountsLines(p gotermstyle.Palette, initiatives []api.Initiative) map[int]string {
	lines := make(map[int]string, len(initiatives))
	for _, initiative := range initiatives {
		line := fmt.Sprintf("#%d %s (%s) · %d open, %d done, %d canceled",
			initiative.Number, initiative.Name, initiative.Status, initiative.OpenCount, initiative.CompletedCount, initiative.CanceledCount)
		if checked := paintedCheckedSteps(p, initiative.Steps); checked != "" {
			line += " · " + checked
		}
		lines[initiative.Number] = line
	}
	return lines
}
