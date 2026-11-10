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

var pursuitHints = []string{
	anyRecordHint,
	"Pursuits that are not active are hidden: slate pursuits list --status all",
}

// The help each kind's --pursuit carries, on a create, an edit and a list.
const (
	pursuitSetHelp     = "Pursuit it serves, by number or exact name (repeatable; see: slate pursuits list)"
	pursuitReplaceHelp = "Pursuit, by number or exact name (repeatable; replaces the set, empty removes every pursuit)"
	pursuitFilterHelp  = "Only records in this pursuit, by number or exact name, an issue through its initiative (see: slate pursuits list)"
)

// pursuitCreateStatuses are where a new pursuit may start.
var pursuitCreateStatuses = []string{"active", "someday"}

// pursuitNumbers resolves each --pursuit to its number. A number passes as
// it is, so the API refuses one naming no pursuit. A name is read by the API,
// which names the near ones where it matches none.
func pursuitNumbers(cmd *cobra.Command, client *api.Client, refs []string) ([]int, error) {
	numbers := make([]int, 0, len(refs))
	for _, ref := range refs {
		if digits := strings.TrimPrefix(ref, "#"); isItemNumber(digits) {
			number, err := strconv.Atoi(digits)
			if err != nil {
				return nil, goclikit.UsageError(fmt.Errorf("--pursuit %q is not a number the API can read: %w", ref, err))
			}
			numbers = append(numbers, number)
			continue
		}
		pursuit, err := client.GetPursuit(cmd.Context(), ref)
		if err != nil {
			return nil, handleArgumentAPIError(err)
		}
		numbers = append(numbers, pursuit.Number)
	}
	return numbers, nil
}

// pursuitsEdited reads an edit's --pursuit as its numbers: nil where it was
// not passed, an empty set where it was passed empty, and the numbers otherwise.
func pursuitsEdited(cmd *cobra.Command, client *api.Client, f *pflag.FlagSet, refs []string) (*[]int, error) {
	switch readFlagIntent(f, "pursuit") {
	case flagClears:
		return &[]int{}, nil
	case flagCarriesValues:
		numbers, err := pursuitNumbers(cmd, client, refs)
		if err != nil {
			return nil, err
		}
		return &numbers, nil
	case flagMixesEmptyWithValues:
		return nil, goclikit.UsageError(errors.New("--pursuit given both a value and an empty one — an empty value removes every pursuit, so pass one or the other"))
	case flagUntouched:
	}
	return nil, nil
}

// An API from before pursuits drops --pursuit from a request and answers
// as though it held it. Each check below refuses such an answer by the
// pursuits it carries.

// inPursuit says whether pursuits holds the one ref names, by number or
// by exact name.
func inPursuit(ref string, pursuits []api.PursuitSummary) bool {
	digits := strings.TrimPrefix(ref, "#")
	return slices.ContainsFunc(pursuits, func(pursuit api.PursuitSummary) bool {
		if isItemNumber(digits) {
			return strconv.Itoa(pursuit.Number) == digits
		}
		return pursuit.Name == ref
	})
}

// pursuitNamed is a --pursuit as an error names it: #N for a number, and
// the quoted name otherwise.
func pursuitNamed(ref string) string {
	if digits := strings.TrimPrefix(ref, "#"); isItemNumber(digits) {
		return "#" + digits
	}
	return strconv.Quote(ref)
}

// honoredPursuitWrite refuses a write whose answer is in other pursuits
// than the --pursuit it was sent. A nil want sent none.
func honoredPursuitWrite(want *[]int, number int, carried []api.PursuitSummary) error {
	if want == nil {
		return nil
	}
	got := make([]int, 0, len(carried))
	for _, pursuit := range carried {
		got = append(got, pursuit.Number)
	}
	wanted := slices.Clone(*want)
	slices.Sort(wanted)
	if slices.Equal(slices.Compact(wanted), got) {
		return nil
	}
	return fmt.Errorf("the API ignored --pursuit: #%d was written in %s rather than %s, so it predates pursuits",
		number, pursuitNumbersOrNone(got), pursuitNumbersOrNone(wanted))
}

// pursuitList is each pursuit by number and name, as a detail line reads it.
func pursuitList(pursuits []api.PursuitSummary) string {
	shown := make([]string, 0, len(pursuits))
	for _, pursuit := range pursuits {
		shown = append(shown, fmt.Sprintf("#%d %s", pursuit.Number, pursuit.Name))
	}
	return strings.Join(shown, ", ")
}

func pursuitNumbersOrNone(numbers []int) string {
	if len(numbers) == 0 {
		return "no pursuit"
	}
	shown := make([]string, 0, len(numbers))
	for _, number := range numbers {
		shown = append(shown, "#"+strconv.Itoa(number))
	}
	return strings.Join(shown, ", ")
}

func newPursuitsCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "pursuits",
		Short: "Name the conditions the work should bring about and hold, and the ideas and initiatives serving each",
		Long: "A pursuit is a condition the work should bring about and hold, read from what\n" +
			"is measured rather than from time spent. --measure names the command that reads\n" +
			"where it stands. Ideas and initiatives join pursuits with --pursuit, and an\n" +
			"issue reads its initiative's. A pursuit holds no work, so `slate next` never\n" +
			"lists one and no claim holds one, and it lends no priority. Effort spread\n" +
			"thin moves none of them, so `slate pursuits list` counts the active ones.",
		Example: "  slate pursuits list                                         the active ones, with what is in each\n" +
			"  slate pursuits create --name \"Data Warehouse\" --summary \"All data flows through the warehouse\" --measure \"warehouse-coverage --json\"\n" +
			"  slate initiatives edit 2653 --pursuit \"Data Warehouse\"\n" +
			"  slate issues list --pursuit \"Data Warehouse\"",
	})
	goclikit.WithRecoveryHints(cmd, pursuitHints...)
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupReading,
		newPursuitsListCommand(),
		newPursuitsShowCommand(),
	)...)
	cmd.AddCommand(inGroup(groupChanging,
		newPursuitsCreateCommand(),
		newPursuitsEditCommand(),
		newPursuitsCompleteCommand(),
		newPursuitsDropCommand(),
		newPursuitsShelveCommand(),
		newPursuitsReopenCommand(),
	)...)
	return cmd
}

func newPursuitsListCommand() *cobra.Command {
	var (
		asJSON        bool
		pursuitStatus string
		limit         int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the pursuits with the ideas and initiatives in each, active first",
		Long: "The header counts the active pursuits. INITIATIVES and IDEAS count every one\n" +
			"in the pursuit, whatever its status. MEASURE is the command that reads where\n" +
			"it stands, and - where nothing measures it yet.",
		Example:     "  slate pursuits list\n  slate pursuits list --status all --json",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("status") && !slices.Contains(api.PursuitStatuses, pursuitStatus) {
				return goclikit.UsageError(fmt.Errorf("unknown status %q — one of: %s", pursuitStatus, strings.Join(api.PursuitStatuses, ", ")))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			pursuits, err := client.ListPursuits(cmd.Context(), pursuitStatus, limitFlag(cmd))
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), pursuits)
			}
			if !cmd.Flags().Changed("status") {
				defer noteHiddenByStatus(cmd, "Pursuits that are not active")
			}
			printPursuits(cmd.OutOrStdout(), pursuits)
			return nil
		},
	}
	cmd.Flags().StringVar(&pursuitStatus, "status", "", "One of: "+strings.Join(api.PursuitStatuses, ", ")+" (default active)")
	withFlagValues(cmd, "status", api.PursuitStatuses...)
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output pursuits as JSON to stdout")
	addLimitFlag(cmd, &limit)
	return cmd
}

// pursuitShown is a pursuit, the initiatives list's row for each initiative in it,
// with its counts and checked steps, and the changes its show read. InitiativeRows is
// nil where its read failed, and InitiativeRowsError says why.
type pursuitShown struct {
	api.PursuitDetail
	InitiativeRows      []api.Initiative `json:"initiative_rows"`
	InitiativeRowsError string           `json:"initiative_rows_error,omitempty"`
	Changes             *recordChanges   `json:"changes,omitempty"`
}

// readPursuitShown reads the row of each initiative in the pursuit, whatever its
// status. The rows are one section of the show, so a failed read leaves the pursuit
// shown, each initiative by its number and name, and says why.
func readPursuitShown(cmd *cobra.Command, client *api.Client, pursuit api.PursuitDetail) pursuitShown {
	shown := pursuitShown{PursuitDetail: pursuit, InitiativeRows: []api.Initiative{}}
	if len(pursuit.Initiatives) == 0 {
		return shown
	}
	filter := api.InitiativeFilter{Status: "all", Pursuit: strconv.Itoa(pursuit.Number)}
	initiatives, err := client.ListInitiatives(cmd.Context(), filter, nil)
	if err != nil {
		shown.InitiativeRows = nil
		shown.InitiativeRowsError = fmt.Sprintf("each initiative's counts and steps were not read: %v", handleAPIError(err))
		return shown
	}
	shown.InitiativeRows = initiatives
	return shown
}

// noteInitiativeRows says on stderr why a show prints no counts or steps.
func noteInitiativeRows(cmd *cobra.Command, shown pursuitShown) {
	if shown.InitiativeRowsError != "" {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s\n", shown.InitiativeRowsError)
	}
}

func newPursuitsShowCommand() *cobra.Command {
	var (
		asJSON      bool
		changeCount int
	)
	cmd := &cobra.Command{
		Use:   "show <pursuit>",
		Short: "Show a pursuit, each idea and initiative in it, and its changes",
		Long: "A pursuit is named by its number or its exact name. Its measure prints, or none\n" +
			"where nothing measures it yet. Each initiative in it, whatever its status, prints\n" +
			"with its open, done and canceled issues and the steps checked on it. Each idea\n" +
			"prints by number and name.",
		Example:     "  slate pursuits show \"Data Warehouse\"\n  slate pursuits show 2950 --json",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			pursuit, err := client.GetPursuit(cmd.Context(), args[0])
			if err != nil {
				return handleArgumentAPIError(err)
			}
			shown := readPursuitShown(cmd, client, pursuit)
			defer noteInitiativeRows(cmd, shown)
			if shown.Changes, err = readChanges(cmd, client, pursuit.Number, changeCount, asJSON); err != nil {
				return err
			}
			defer noteChanges(cmd, shown.Changes)
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), shown)
			}
			printPursuitShown(cmd.OutOrStdout(), shown, time.Now())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the pursuit as JSON to stdout")
	addChangesFlag(cmd, &changeCount)
	return cmd
}

func newPursuitsCreateCommand() *cobra.Command {
	var (
		name, summary, description, measure, pursuitStatus string
		asJSON                                             bool
	)
	cmd := &cobra.Command{
		Use:   "create --name <name> --summary <what it means> [flags]",
		Short: "Name a condition the work should bring about and hold",
		Long: "--summary says what the condition means, on one line, and is required.\n" +
			"--measure names the command that reads where it stands, on one line. An\n" +
			"active pursuit's name is its own, so a second active one by that name is\n" +
			"refused. --status someday names it without taking it up, and `slate pursuits\n" +
			"reopen` takes it up later.",
		Example:     "  slate pursuits create --name \"Data Warehouse\" --summary \"All data flows through the warehouse, and every tool queries its tables\"",
		Args:        cobra.NoArgs,
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(name) == "" {
				return goclikit.UsageError(errors.New("--name is required"))
			}
			if strings.TrimSpace(summary) == "" {
				return goclikit.UsageError(errors.New("--summary is required — say what the condition means, on one line"))
			}
			f := cmd.Flags()
			if f.Changed("status") && !slices.Contains(pursuitCreateStatuses, pursuitStatus) {
				return goclikit.UsageError(fmt.Errorf("unknown status %q — one of: %s", pursuitStatus, strings.Join(pursuitCreateStatuses, ", ")))
			}
			in := api.PursuitCreateInput{
				Name: name, Summary: summary, Description: valuedStrFlag(f, "description", &description),
				Measure: valuedStrFlag(f, "measure", &measure), Status: nonEmpty(pursuitStatus),
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.CreatePursuit(cmd.Context(), in)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, pursuitLine("Named", written.Pursuit))
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Pursuit name, unique among active ones (required)")
	cmd.Flags().StringVar(&summary, "summary", "", "What the condition means, on one line (required)")
	cmd.Flags().StringVar(&description, "description", "", "The pursuit in full")
	cmd.Flags().StringVar(&measure, "measure", "", "The command that reads where it stands, on one line")
	cmd.Flags().StringVar(&pursuitStatus, "status", "", "One of: "+strings.Join(pursuitCreateStatuses, ", ")+" (default active)")
	withFlagValues(cmd, "status", pursuitCreateStatuses...)
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newPursuitsEditCommand() *cobra.Command {
	var (
		name, summary, description, measure string
		asJSON                              bool
	)
	cmd := &cobra.Command{
		Use:   "edit <pursuit> [flags]",
		Short: "Rename a pursuit, or change its summary, description or measure",
		Long: "Update only the fields whose flags you pass. --description \"\" and --measure \"\"\n" +
			"each clear their field. Status moves through complete, drop, shelve and reopen.",
		Example: "  slate pursuits edit \"Data Warehouse\" --name \"Warehouse\"\n" +
			"  slate pursuits edit 2950 --summary \"Every tool reads the warehouse\"\n" +
			"  slate pursuits edit 2950 --measure \"warehouse-coverage --json\"",
		SuggestFor:  editMistakenFor,
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			in := api.PursuitUpdateInput{
				Name:        valuedStrFlag(f, "name", &name),
				Summary:     valuedStrFlag(f, "summary", &summary),
				Description: valuedStrFlag(f, "description", &description),
				Measure:     valuedStrFlag(f, "measure", &measure),
			}
			var clear []string
			for _, field := range []string{"description", "measure"} {
				if readFlagIntent(f, field) == flagClears {
					clear = append(clear, field)
				}
			}
			if in == (api.PursuitUpdateInput{}) && len(clear) == 0 {
				return goclikit.UsageError(errors.New("nothing to change — pass at least one of --name/--summary/--description/--measure"))
			}
			return runPursuitUpdate(cmd, args[0], in, clear, asJSON, "Updated")
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New name")
	cmd.Flags().StringVar(&summary, "summary", "", "What the condition means, on one line")
	cmd.Flags().StringVar(&description, "description", "", "New description (empty clears)")
	cmd.Flags().StringVar(&measure, "measure", "", "The command that reads where it stands, on one line (empty clears)")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newPursuitsShelveCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "shelve <pursuit>",
		Short: "Set a pursuit aside for someday, out of the active count",
		Long: "For a condition that is named but not taken up. Every idea and initiative in\n" +
			"it stays in it, and it gives up its name, so a new pursuit may take it.\n" +
			"`slate pursuits reopen` takes it up again.",
		Example:     "  slate pursuits shelve \"Mobile App\"",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			someday := "someday"
			return runPursuitUpdate(cmd, args[0], api.PursuitUpdateInput{Status: &someday}, nil, asJSON, "Shelved")
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newPursuitsReopenCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:         "reopen <pursuit>",
		Short:       "Return a shelved or finished pursuit to the active list",
		Long:        "Refused if an active pursuit has taken the name in the meantime.",
		Example:     "  slate pursuits reopen \"Mobile App\"",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			active := "active"
			return runPursuitUpdate(cmd, args[0], api.PursuitUpdateInput{Status: &active}, nil, asJSON, "Reopened")
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newPursuitsCompleteCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:         "complete <pursuit>",
		Short:       "Mark a pursuit held, which hides it",
		Long:        "Every idea and initiative in it stays in it, and keeps its own status.",
		Example:     "  slate pursuits complete \"Data Warehouse\"",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			completed := "completed"
			return runPursuitUpdate(cmd, args[0], api.PursuitUpdateInput{Status: &completed}, nil, asJSON, "Completed")
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newPursuitsDropCommand() *cobra.Command {
	var (
		reason string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:         "drop <pursuit> --reason <why>",
		Short:       "Close a pursuit the work will no longer serve",
		Long:        "--reason is required, so nobody takes it up again without reading why.",
		Example:     "  slate pursuits drop \"Mobile App\" --reason \"Folded into Web Client\"",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(reason) == "" {
				return goclikit.UsageError(errors.New("--reason is required — say why it is dropped"))
			}
			dropped := "dropped"
			return runPursuitUpdate(cmd, args[0], api.PursuitUpdateInput{Status: &dropped, StatusReason: &reason}, nil, asJSON, "Dropped")
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Why it is dropped (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func runPursuitUpdate(cmd *cobra.Command, ref string, in api.PursuitUpdateInput, clear []string, asJSON bool, verb string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return handleAPIError(err)
	}
	written, err := client.UpdatePursuit(cmd.Context(), ref, in, clear)
	if err != nil {
		return handleArgumentAPIError(err)
	}
	return reportWrite(cmd, asJSON, written, written.WriteEffects, pursuitLine(verb, written.Pursuit))
}

func pursuitLine(verb string, pursuit api.Pursuit) string {
	return fmt.Sprintf("%s pursuit #%d %q (%s)", verb, pursuit.Number, pursuit.Name, pursuit.Status)
}

func printPursuits(out io.Writer, pursuits []api.Pursuit) {
	active := 0
	for _, pursuit := range pursuits {
		if pursuit.Status == "active" {
			active++
		}
	}
	_, _ = fmt.Fprintf(out, "%d active %s\n\n", active, plural(active, "pursuit", "pursuits"))
	if len(pursuits) == 0 {
		_, _ = fmt.Fprintln(out, "No pursuits. Name one with `slate pursuits create --name ... --summary ...`.")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NUMBER\tSTATUS\tINITIATIVES\tIDEAS\tNAME\tSUMMARY\tMEASURE")
	for _, pursuit := range pursuits {
		measure := strValue(pursuit.Measure)
		if measure == "" {
			measure = "-"
		}
		_, _ = fmt.Fprintf(tw, "#%d\t%s\t%d\t%d\t%s\t%s\t%s\n",
			pursuit.Number, pursuit.Status, len(pursuit.Initiatives), len(pursuit.Ideas), pursuit.Name, pursuit.Summary, measure)
	}
	_ = tw.Flush()
}

// printPursuitShown is what every show of a pursuit prints.
func printPursuitShown(out io.Writer, shown pursuitShown, now time.Time) {
	printPursuitDetail(out, shown.Pursuit, shown.InitiativeRows)
	printRecordParts(out, shown.RecordParts, now)
	printChanges(out, shown.Changes, now)
}

func printPursuitDetail(out io.Writer, pursuit api.Pursuit, counted []api.Initiative) {
	p := gotermstyle.PaletteFor(out)
	_, _ = fmt.Fprintf(out, "%s  %s\n", p.Cyan("#"+strconv.Itoa(pursuit.Number)), p.Bold(pursuit.Name))
	_, _ = fmt.Fprintf(out, "  summary:      %s\n", pursuit.Summary)
	measure := strValue(pursuit.Measure)
	if measure == "" {
		measure = "none"
	}
	_, _ = fmt.Fprintf(out, "  measure:      %s\n", measure)
	status := pursuit.Status
	if pursuit.ClosedTS != nil {
		status += " (" + localDay(*pursuit.ClosedTS) + ")"
	}
	_, _ = fmt.Fprintf(out, "  status:       %s\n", status)
	if reason := strValue(pursuit.StatusReason); reason != "" {
		_, _ = fmt.Fprintf(out, "  reason:       %s\n", reason)
	}
	printMembers(out, "initiatives:", pursuit.Initiatives, initiativeCountsLines(p, counted))
	printMembers(out, "ideas:", pursuit.Ideas, nil)
	if description := strValue(pursuit.Description); description != "" {
		_, _ = fmt.Fprintf(out, "\nDescription:\n%s\n", indent(description, "  "))
	}
}

// printMembers prints each record in a pursuit a line under the label, with
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
