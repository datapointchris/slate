package cli

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/gotermboard"
	"github.com/datapointchris/gotermstyle"
	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

var initiativeHints = []string{
	anyRecordHint,
	"Someday, completed and dropped initiatives are hidden: slate initiatives list --status all",
}

func newInitiativesCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "initiatives",
		Short: "Group the issues that ship one outcome",
		Long: "An initiative is an outcome that finishes. Most issues belong to none. An\n" +
			"issue sorts no lower than its active initiative's priority. A finished\n" +
			"initiative takes no new issues and leaves the ones it holds as they are. A\n" +
			"someday initiative is held back until it is reopened, and so are its issues,\n" +
			"except one that work outside someday depends on. An initiative starts with\n" +
			"autofix off, so an unattended fix never lands one of its steps alone.\n" +
			"An initiative shares its number sequence with ideas and issues,\n" +
			"and an idea's promotion creates one.",
		Example: "  slate initiatives list                         the active ones, in the order to work them\n" +
			"  slate initiatives progress \"Ship the tracker\"  its bar, what runs, waits and comes next\n" +
			"  slate initiatives show 1501                    its claims, unclosed issues and changes\n" +
			"  slate initiatives complete \"Ship the tracker\"  close it once the outcome ships",
	})
	goclikit.WithRecoveryHints(cmd, initiativeHints...)
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupReading,
		newInitiativesListCommand(),
		newInitiativesShowCommand(),
		newInitiativesProgressCommand(),
		newInitiativesBoardCommand(),
	)...)
	cmd.AddCommand(inGroup(groupChanging,
		newInitiativesCreateCommand(),
		newInitiativesEditCommand(),
		newInitiativesCompleteCommand(),
		newInitiativesDropCommand(),
		newInitiativesShelveCommand(),
		newInitiativesReopenCommand(),
		newInitiativesReorderCommand(),
		newInitiativesPinCommand(),
		newInitiativesUnpinCommand(),
		newInitiativesDeleteCommand(),
	)...)
	return cmd
}

func newInitiativesReorderCommand() *cobra.Command {
	return newReorderCommand("initiative",
		"An initiative's issues move with it.",
		"  slate initiatives reorder \"Ship the tracker\" --before 1491\n  slate initiatives reorder 1502 --after 1491")
}

func newInitiativesListCommand() *cobra.Command {
	var (
		asJSON           bool
		initiativeStatus string
		sort             string
		tag              string
		pursuit          string
		limit            int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the active initiatives in the order to work them",
		Long: "Active initiatives by priority, then rank, under a header for each tier.\n" +
			"Someday, completed and dropped ones are hidden until --status asks for them,\n" +
			"and come after the active ones, finished ones latest first.\n\n" +
			"Each row leads with the initiative's number, which every initiative command\n" +
			"and --initiative flag accepts. The bar is its issues done over done and open.\n" +
			"Once a step of its life is checked, a second line marks each step, a checked\n" +
			"one with the day its worklog ended.\n\n" +
			"`slate tui` opens the same initiatives as a board that keeps itself current.",
		Example:     "  slate initiatives list\n  slate initiatives list --sort open --tag security\n  slate initiatives list --status all --json",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("status") && !slices.Contains(api.InitiativeStatuses, initiativeStatus) {
				return goclikit.UsageError(fmt.Errorf("unknown status %q — one of: %s", initiativeStatus, strings.Join(api.InitiativeStatuses, ", ")))
			}
			if cmd.Flags().Changed("sort") && !slices.Contains(api.InitiativeSorts, sort) {
				return goclikit.UsageError(fmt.Errorf("unknown sort %q — one of: %s", sort, strings.Join(api.InitiativeSorts, ", ")))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			filter := api.InitiativeFilter{Status: initiativeStatus, Sort: sort, Tag: tag, Pursuit: pursuit}
			initiatives, err := client.ListInitiatives(cmd.Context(), filter, limitFlag(cmd))
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if err := honoredGroupings(tag, pursuit, initiatives, initiativeGrouped); err != nil {
				return err
			}
			if !cmd.Flags().Changed("status") {
				defer noteHiddenByStatus(cmd, "Someday, completed and dropped initiatives")
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), initiatives)
			}
			printInitiativesTable(cmd.OutOrStdout(), initiatives, sort)
			return nil
		},
	}
	cmd.Flags().StringVar(&initiativeStatus, "status", "", "One of: "+strings.Join(api.InitiativeStatuses, ", ")+" (default active)")
	withFlagValues(cmd, "status", api.InitiativeStatuses...)
	cmd.Flags().StringVar(&sort, "sort", "", "One of: "+strings.Join(api.InitiativeSorts, ", ")+" (default priority); open puts the most unclosed issues first")
	withFlagValues(cmd, "sort", api.InitiativeSorts...)
	cmd.Flags().StringVar(&tag, "tag", "", "Only initiatives carrying this tag (see: slate tags list)")
	cmd.Flags().StringVar(&pursuit, "pursuit", "", "Only initiatives in this pursuit, by number or exact name (see: slate pursuits list)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output initiatives as JSON to stdout")
	addLimitFlag(cmd, &limit)
	return cmd
}

func newInitiativesShowCommand() *cobra.Command {
	var (
		asJSON      bool
		changeCount int
	)
	cmd := &cobra.Command{
		Use:         "show <initiative>",
		Short:       "Show an initiative, its claims, its unclosed issues in queue order, and its changes",
		Example:     "  slate initiatives show 1501\n  slate initiatives show \"Ship the tracker\"",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			initiative, err := client.GetInitiative(cmd.Context(), args[0])
			if err != nil {
				return handleArgumentAPIError(err)
			}
			shown, err := readInitiativeIssues(cmd, client, initiative)
			if err != nil {
				return err
			}
			if shown.Changes, err = readChanges(cmd, client, initiative.Number, changeCount, asJSON); err != nil {
				return err
			}
			defer noteChanges(cmd, shown.Changes)
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), shown)
			}
			printInitiativeShown(cmd, shown)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the initiative and its issues as JSON to stdout")
	addChangesFlag(cmd, &changeCount)
	return cmd
}

// initiativeShown is an initiative beside its unclosed issues, and the changes
// the show read, as every show of one prints it.
type initiativeShown struct {
	api.InitiativeDetail
	Issues  []api.Issue    `json:"issues"`
	Changes *recordChanges `json:"changes,omitempty"`
}

// readInitiativeIssues reads the unclosed issues an initiative's show lists.
func readInitiativeIssues(cmd *cobra.Command, client *api.Client, initiative api.InitiativeDetail) (initiativeShown, error) {
	// The filter is the initiative's id from the read before, never the
	// caller's argument, so a refusal here is not theirs to fix.
	issues, err := client.ListIssues(cmd.Context(), api.IssueFilter{Initiative: initiative.ID}, api.Window{}, issueZone(), nil)
	if err != nil {
		return initiativeShown{}, handleAPIError(err)
	}
	return initiativeShown{InitiativeDetail: initiative, Issues: issues}, nil
}

func printInitiativeShown(cmd *cobra.Command, shown initiativeShown) {
	out, now := cmd.OutOrStdout(), time.Now()
	printInitiativeDetail(out, shown.Initiative)
	printRecordParts(out, shown.RecordParts, now)
	_, _ = fmt.Fprintf(out, "\nUnclosed issues (%d):\n", len(shown.Issues))
	if len(shown.Issues) == 0 {
		_, _ = fmt.Fprintln(out, "  (none)")
	} else {
		printIssuesTable(out, shown.Issues, now)
		noteInheritedPriority(cmd, shown.Issues)
	}
	printChanges(out, shown.Changes, now)
}

func newInitiativesCreateCommand() *cobra.Command {
	var (
		name        string
		summary     string
		description string
		priority    string
		autofix     bool
		tags        []string
		pursuits    []string
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "create --name <name> --summary <summary> [flags]",
		Short: "Start an initiative",
		Long: "The initiative ranks last among the records of its priority, and\n" +
			"`slate initiatives reorder` moves it.",
		Example: "  slate initiatives create --name \"Ship the tracker\" --summary \"Every project's work lives in it\" --priority high\n" +
			"  slate initiatives create --name \"Build day\" --summary \"A day runs from one ordered stack\" --autofix=false",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsPlan),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(name) == "" {
				return goclikit.UsageError(errors.New("--name is required"))
			}
			if strings.TrimSpace(summary) == "" {
				return goclikit.UsageError(errors.New("--summary is required: " + initiativeSummaryHelp))
			}
			f := cmd.Flags()
			in := api.InitiativeCreateInput{Name: name, Summary: summary, Description: valuedStrFlag(f, "description", &description), Tags: tags}
			if f.Changed("priority") {
				value, err := parsePriority(priority)
				if err != nil {
					return goclikit.UsageError(fmt.Errorf("--priority: %w", err))
				}
				in.Priority = &value
			}
			if f.Changed("autofix") {
				in.Autofix = &autofix
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			if in.Pursuits, err = pursuitNumbers(cmd, client, pursuits); err != nil {
				return err
			}
			written, err := client.CreateInitiative(cmd.Context(), in)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			initiative := written.Initiative
			if err := reportWrite(cmd, asJSON, written, written.WriteEffects, fmt.Sprintf("Started initiative #%d %q", initiative.Number, initiative.Name)); err != nil {
				return err
			}
			if err := honoredTagWrite(&in.Tags, initiative.Number, initiative.Tags); err != nil {
				return err
			}
			return honoredPursuitWrite(&in.Pursuits, initiative.Number, initiative.Pursuits)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Initiative name, unique among active ones (required)")
	cmd.Flags().StringVar(&summary, "summary", "", initiativeSummaryHelp+" (required)")
	cmd.Flags().StringVar(&description, "description", "", "The outcome, and how you will know it is done")
	cmd.Flags().StringVar(&priority, "priority", "", "One of: "+priorityChoices()+" (default none)")
	withFlagValues(cmd, "priority", api.IssuePriorityNames...)
	cmd.Flags().BoolVar(&autofix, "autofix", false, initiativeAutofixHelp)
	cmd.Flags().StringArrayVar(&tags, "tag", nil, tagSetHelp+"; its issues read it too")
	cmd.Flags().StringArrayVar(&pursuits, "pursuit", nil, pursuitSetHelp+"; its issues read it too")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newInitiativesEditCommand() *cobra.Command {
	var (
		name        string
		summary     string
		description string
		priority    string
		autofix     bool
		tags        []string
		pursuits    []string
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "edit <initiative> [flags]",
		Short: "Change an initiative's fields",
		Long: "Update only the fields whose flags you pass. --description \"\" clears it.\n" +
			"--tag and --pursuit each replace the whole set, and --tag \"\" or\n" +
			"--pursuit \"\" removes every one; each of its issues reads the new set as its\n" +
			"own too. Status moves through complete, drop, shelve and reopen, and its place\n" +
			"in the queue through reorder.",
		Example: "  slate initiatives edit \"Ship the tracker\" --priority urgent\n" +
			"  slate initiatives edit 1491 --autofix=false\n" +
			"  slate initiatives edit 1491 --tag security --pursuit \"Data Warehouse\"",
		SuggestFor:  editMistakenFor,
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeRecordsPlan),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			if readFlagIntent(f, "name") == flagClears {
				return goclikit.UsageError(errors.New("--name cannot be emptied — every initiative carries one"))
			}
			if readFlagIntent(f, "summary") == flagClears {
				return goclikit.UsageError(errors.New("--summary cannot be emptied — every initiative carries one"))
			}
			in := api.InitiativeUpdateInput{
				Name:        valuedStrFlag(f, "name", &name),
				Summary:     valuedStrFlag(f, "summary", &summary),
				Description: valuedStrFlag(f, "description", &description),
			}
			if f.Changed("priority") {
				value, err := parsePriority(priority)
				if err != nil {
					return goclikit.UsageError(fmt.Errorf("--priority: %w", err))
				}
				in.Priority = &value
			}
			if f.Changed("autofix") {
				in.Autofix = &autofix
			}
			tagSet, err := tagsEdited(f, tags)
			if err != nil {
				return err
			}
			in.Tags = tagSet
			var clear []string
			if readFlagIntent(f, "description") == flagClears {
				clear = append(clear, "description")
			}
			if in == (api.InitiativeUpdateInput{}) && len(clear) == 0 && !f.Changed("pursuit") {
				return goclikit.UsageError(errors.New("nothing to change — pass at least one of --name/--summary/--description/--priority/--autofix/--tag/--pursuit"))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			if in.Pursuits, err = pursuitsEdited(cmd, client, f, pursuits); err != nil {
				return err
			}
			return writeInitiativeUpdate(cmd, client, args[0], in, clear, asJSON, "Updated")
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New name")
	cmd.Flags().StringVar(&summary, "summary", "", initiativeSummaryHelp)
	cmd.Flags().StringVar(&description, "description", "", "New description (empty clears)")
	cmd.Flags().StringVar(&priority, "priority", "", "One of: "+priorityChoices())
	withFlagValues(cmd, "priority", api.IssuePriorityNames...)
	cmd.Flags().BoolVar(&autofix, "autofix", false, initiativeAutofixHelp)
	cmd.Flags().StringArrayVar(&tags, "tag", nil, tagReplaceHelp)
	cmd.Flags().StringArrayVar(&pursuits, "pursuit", nil, pursuitReplaceHelp)
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

const initiativeSummaryHelp = "What is true when it is done, on one line of at most 240 characters, in words the name does not use"

const initiativeAutofixHelp = "True opens its issues to an unattended fix; false, where it starts, keeps them for a session"

func runInitiativeUpdate(cmd *cobra.Command, ref string, in api.InitiativeUpdateInput, clear []string, asJSON bool, verb string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return handleAPIError(err)
	}
	return writeInitiativeUpdate(cmd, client, ref, in, clear, asJSON, verb)
}

func writeInitiativeUpdate(
	cmd *cobra.Command, client *api.Client, ref string, in api.InitiativeUpdateInput, clear []string, asJSON bool, verb string,
) error {
	written, err := client.UpdateInitiative(cmd.Context(), ref, in, clear, issueZone())
	if err != nil {
		return handleArgumentAPIError(err)
	}
	initiative := written.Initiative
	summary := fmt.Sprintf("%s initiative #%d %q (%s)", verb, initiative.Number, initiative.Name, initiative.Status)
	if err := reportWrite(cmd, asJSON, written, written.WriteEffects, summary); err != nil {
		return err
	}
	if err := honoredTagWrite(in.Tags, initiative.Number, initiative.Tags); err != nil {
		return err
	}
	return honoredPursuitWrite(in.Pursuits, initiative.Number, initiative.Pursuits)
}

func newInitiativesCompleteCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "complete <initiative>",
		Short: "Mark an initiative's outcome reached, which hides it",
		Long: "Its issues keep their status, so one still open stays in the queue. It ends\n" +
			"the initiative's claim as completed, and prints whose claim it ended.",
		Example:     "  slate initiatives complete \"Ship the tracker\"",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeRecordsPlan),
		RunE: func(cmd *cobra.Command, args []string) error {
			completed := "completed"
			return runInitiativeUpdate(cmd, args[0], api.InitiativeUpdateInput{Status: &completed}, nil, asJSON, "Completed")
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newInitiativesDropCommand() *cobra.Command {
	var (
		reason string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "drop <initiative> --reason <why>",
		Short: "Close an initiative whose outcome you no longer want",
		Long: "--reason is required. A drop with its reason closes the question, and a bare\n" +
			"drop invites it back. It ends the initiative's claim as dropped, and prints\n" +
			"whose claim it ended.",
		Example:     "  slate initiatives drop \"Rewrite the router\" --reason \"Traefik covers it\"",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeRecordsPlan),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(reason) == "" {
				return goclikit.UsageError(errors.New("--reason is required — say why it is dropped"))
			}
			dropped := "dropped"
			return runInitiativeUpdate(cmd, args[0], api.InitiativeUpdateInput{Status: &dropped, StatusReason: &reason}, nil, asJSON, "Dropped")
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Why it is dropped (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newInitiativesShelveCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "shelve <initiative>",
		Short: "Set an initiative aside for someday, so none of its issues is ready",
		Long: "For work planned in full but not taken on. It moves to --status someday,\n" +
			"out of the active list, and no close is recorded. Its issues keep their\n" +
			"state, but `slate next` leaves them out, so no agent takes them. One\n" +
			"that work outside someday depends on stays in, at that work's priority.\n" +
			"It lends no priority, and gives up its name, so a new initiative may take\n" +
			"it. `slate initiatives reopen` makes its issues ready again.",
		Example:     "  slate initiatives shelve \"Measure everything\"",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeRecordsPlan),
		RunE: func(cmd *cobra.Command, args []string) error {
			someday := "someday"
			return runInitiativeUpdate(cmd, args[0], api.InitiativeUpdateInput{Status: &someday}, nil, asJSON, "Shelved")
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newInitiativesReopenCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:         "reopen <initiative>",
		Short:       "Return a shelved or finished initiative to the active list",
		Long:        "Refused if an active initiative has taken the name in the meantime.",
		Example:     "  slate initiatives reopen \"Ship the tracker\"",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeRecordsPlan),
		RunE: func(cmd *cobra.Command, args []string) error {
			active := "active"
			return runInitiativeUpdate(cmd, args[0], api.InitiativeUpdateInput{Status: &active}, nil, asJSON, "Reopened")
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newInitiativesDeleteCommand() *cobra.Command {
	var (
		yes    bool
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "delete <initiative> [--yes]",
		Short: "Delete an initiative and its claims, leaving its issues in none",
		Long: "Every claim it has had goes with it, live or ended, with a warning when\n" +
			"another holder's live claim was one. It prints each claim it took and each\n" +
			"issue it left in no initiative. Prompts unless --yes.",
		Example:     "  slate initiatives delete \"Ship the tracker\" --yes",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			initiative, err := client.GetInitiative(cmd.Context(), args[0])
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if !yes {
				question := fmt.Sprintf("Delete initiative #%d %q? Its %d %s will belong to none.",
					initiative.Number, initiative.Name, initiative.IssueCount, plural(initiative.IssueCount, "issue", "issues"))
				ok, err := confirm(cmd, question)
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Aborted.")
					return nil
				}
			}
			deleted, err := client.DeleteInitiative(cmd.Context(), initiative.ID, issueZone())
			if err != nil {
				return handleAPIError(err)
			}
			summary := fmt.Sprintf("Deleted initiative #%d %q", initiative.Number, initiative.Name)
			return reportWrite(cmd, asJSON, deleted, deleted.WriteEffects, summary)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

// progressCells is how many cells the list's bar of done over open-and-done spans.
const progressCells = 10

// progressFilled is how many of a bar's cells are filled. Any finished issue
// fills one, and any open issue keeps one empty, so the bar never reads as
// untouched or finished when it is not.
func progressFilled(done, open, cells int) int {
	total := done + open
	if done == 0 || total == 0 {
		return 0
	}
	filled := (done*cells + total/2) / total
	ceiling := cells
	if open > 0 {
		ceiling--
	}
	return min(max(filled, 1), ceiling)
}

// openKinds splits the open issues into the kinds every bar and the RUN column count.
func openKinds(s api.IssueStateCounts, open int) (underWay, blocked, rest int) {
	underWay = s.Running + s.Waiting
	return underWay, s.Blocked, max(open-underWay-s.Blocked, 0)
}

// initiativeGroupLabel is the header a row sits under in work order: its tier
// while it is active, and its status once it is not.
func initiativeGroupLabel(initiative api.Initiative) string {
	if initiative.Status == "active" {
		return priorityName(initiative.Priority)
	}
	return initiative.Status
}

// initiativeRows renders initiative rows sharing one set of column widths.
type initiativeRows struct {
	palette   gotermstyle.Palette
	clip      bool
	number    int
	done      int
	total     int
	label     int
	byTier    bool
	showStart bool
}

func newInitiativeRows(out io.Writer, initiatives []api.Initiative, sort string) initiativeRows {
	var numbers, done, totals, labels []string
	for _, initiative := range initiatives {
		numbers = append(numbers, strconv.Itoa(initiative.Number))
		done = append(done, strconv.Itoa(initiative.CompletedCount))
		totals = append(totals, strconv.Itoa(initiative.CompletedCount+initiative.OpenCount))
		labels = append(labels, initiativeGroupLabel(initiative))
	}
	return initiativeRows{
		palette:   gotermstyle.PaletteFor(out),
		clip:      clipsToTerminal(out),
		number:    widest(numbers),
		done:      widest(done),
		total:     widest(totals),
		label:     widest(labels),
		byTier:    sort == "" || sort == "priority",
		showStart: sort == "newest",
	}
}

// row lays out one initiative. The number, the bar and the fraction never clip,
// because the number is the handle. When the line runs past the terminal the
// repos give way first, keeping a floor, and then the name.
func (r initiativeRows) row(initiative api.Initiative) string {
	p := r.palette
	open, done := initiative.OpenCount, initiative.CompletedCount
	filled := progressFilled(done, open, progressCells)

	cells := []string{"", paint(p.Cyan, strconv.Itoa(initiative.Number), r.number)}
	used := 2 + r.number
	if !r.byTier {
		label := initiativeGroupLabel(initiative)
		color := tierColor(p, initiative.Priority)
		if initiative.Status != "active" {
			color = p.Magenta
		}
		cells = append(cells, paint(color, label, r.label))
		used += 2 + r.label
	}
	if r.showStart {
		cells = append(cells, localDay(initiative.CreatedTS))
		used += 2 + len(dayLayout)
	}
	bar := gotermboard.TallyBar(filled, 0, 0, progressCells-filled).Paint(p)
	fraction := padLeft(strconv.Itoa(done), r.done) + "/" + padRight(strconv.Itoa(done+open), r.total)
	cells = append(cells, bar, fraction)
	used += 2 + progressCells + 2 + r.done + 1 + r.total

	name, repos := initiative.Name, strings.Join(initiative.Repos, ", ")
	if r.clip {
		name, repos = fitNameAndRepos(name, repos, used+2)
	}
	cells = append(cells, name)
	if repos != "" {
		cells = append(cells, p.Blue(repos))
	}
	return strings.Join(cells, "  ")
}

// lines is the initiative's row, and under it, starting where the row's cells
// after the number do, the marks of its steps once any is checked.
func (r initiativeRows) lines(initiative api.Initiative) string {
	if len(initiative.Steps) == 0 {
		return r.row(initiative)
	}
	return r.row(initiative) + "\n" + strings.Repeat(" ", 4+r.number) + paintedStepMarks(r.palette, initiative.Steps)
}

// repoFloor is the least of a clipped repo list kept beside the name, and
// nameFloor the least of the name kept before the repos are dropped for it.
const (
	repoFloor = 12
	nameFloor = 20
)

// fitNameAndRepos clips the name, which starts at column used, and the repo list
// after it to the terminal. The repos give way first, down to repoFloor, then the
// name. Where the name would keep less than nameFloor the repos are dropped, and
// the name takes the line by gotermstyle.Clip's rule, which leaves it whole when
// there is no room to say anything.
func fitNameAndRepos(name, repos string, used int) (string, string) {
	room := gotermstyle.Columns() - used
	nameLen, reposLen := utf8.RuneCountInString(name), utf8.RuneCountInString(repos)
	if repos == "" {
		return gotermstyle.Clip(name, used), ""
	}
	if nameLen+2+reposLen <= room {
		return name, repos
	}
	repoRoom := max(room-nameLen-2, min(reposLen, repoFloor))
	if room-2-repoRoom < min(nameLen, nameFloor) {
		return gotermstyle.Clip(name, used), ""
	}
	name = gotermstyle.Clip(name, used+2+repoRoom)
	return name, gotermstyle.Clip(repos, used+utf8.RuneCountInString(name)+2)
}

// printInitiativesTable prints each initiative under a header for its tier in
// work order, and as one flat list under any other sort, where each row names
// its own tier. Each row leads with the number, which every initiative command
// accepts.
func printInitiativesTable(out io.Writer, initiatives []api.Initiative, sort string) {
	if len(initiatives) == 0 {
		_, _ = fmt.Fprintln(out, "No initiatives.")
		return
	}
	rows := newInitiativeRows(out, initiatives, sort)
	if !rows.byTier {
		for _, initiative := range initiatives {
			_, _ = fmt.Fprintln(out, rows.lines(initiative))
		}
		return
	}
	p := rows.palette
	for i := 0; i < len(initiatives); {
		label := initiativeGroupLabel(initiatives[i])
		end := i + 1
		for end < len(initiatives) && initiativeGroupLabel(initiatives[end]) == label {
			end++
		}
		if i > 0 {
			_, _ = fmt.Fprintln(out)
		}
		// The color code leads because each one opens with a reset, which would
		// undo a bold set before it.
		code := tierCode(initiatives[i].Priority)
		if initiatives[i].Status != "active" {
			code = gotermstyle.CodeMagenta
		}
		_, _ = fmt.Fprintf(out, "%s · %d\n", p.Paint(code+gotermstyle.CodeBold, label), end-i)
		for _, initiative := range initiatives[i:end] {
			_, _ = fmt.Fprintln(out, rows.lines(initiative))
		}
		i = end
	}
}

func printInitiativeDetail(out io.Writer, initiative api.Initiative) {
	p := gotermstyle.PaletteFor(out)
	_, _ = fmt.Fprintf(out, "%s  %s\n", p.Cyan("#"+strconv.Itoa(initiative.Number)), p.Bold(initiative.Name))
	_, _ = fmt.Fprintf(out, "  summary:    %s\n", initiative.Summary)
	status := initiative.Status
	if initiative.ClosedTS != nil {
		status += " (" + localDay(*initiative.ClosedTS) + ")"
	}
	_, _ = fmt.Fprintf(out, "  status:     %s\n", status)
	if reason := strValue(initiative.StatusReason); reason != "" {
		_, _ = fmt.Fprintf(out, "  reason:     %s\n", reason)
	}
	_, _ = fmt.Fprintf(out, "  priority:   %s\n", tierColor(p, initiative.Priority)(priorityName(initiative.Priority)))
	if !initiative.Autofix {
		_, _ = fmt.Fprintf(out, "  autofix:    off\n")
	}
	_, _ = fmt.Fprintf(out, "  issues:     %d (%d open, %d done, %d canceled)\n",
		initiative.IssueCount, initiative.OpenCount, initiative.CompletedCount, initiative.CanceledCount)
	_, _ = fmt.Fprintf(out, "  repos:      %s\n", repoList(initiative.Repos))
	_, _ = fmt.Fprintf(out, "  steps:      %s\n", paintedStepMarks(p, initiative.Steps))
	if len(initiative.Tags) > 0 {
		_, _ = fmt.Fprintf(out, "  tags:       %s\n", strings.Join(initiative.Tags, ", "))
	}
	if len(initiative.Pursuits) > 0 {
		_, _ = fmt.Fprintf(out, "  pursuits:   %s\n", pursuitList(initiative.Pursuits))
	}
	if initiative.Claim != nil {
		_, _ = fmt.Fprintf(out, "  claim:      %s\n", claimNote(*initiative.Claim, time.Now()))
	}
	if initiative.Idea != nil {
		_, _ = fmt.Fprintf(out, "  idea:       #%d %s\n", initiative.Idea.Number, initiative.Idea.Title)
	}
	printRecordField(out, "waits on:", initiative.DependsOn)
	printRecordField(out, "waited on:", initiative.WaitingInitiatives)
	if description := strValue(initiative.Description); description != "" {
		_, _ = fmt.Fprintf(out, "\nDescription:\n%s\n", indent(description, "  "))
	}
}

// printRecordField prints a record a line under a field's label, the label on
// the first line alone.
func printRecordField(out io.Writer, label string, summaries []api.RecordSummary) {
	for i, summary := range summaries {
		if i > 0 {
			label = ""
		}
		_, _ = fmt.Fprintf(out, "  %-11s %s\n", label, recordSummaryLine(summary))
	}
}
