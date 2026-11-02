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
	"github.com/datapointchris/gotermstyle"
	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

var initiativeHints = []string{"Finished initiatives are hidden: slate issues initiatives list --status all"}

func newIssueInitiativesCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "initiatives",
		Short: "Group the issues that ship one outcome",
		Long: "An initiative is an outcome that finishes. Most issues belong to none. An\n" +
			"issue sorts no lower than its active initiative's priority. A finished\n" +
			"initiative takes no new issues and leaves the ones it holds as they are. A\n" +
			"someday initiative is an idea not yet taken on, and its issues wait until it\n" +
			"is reopened, except one that work outside someday depends on. One worked as\n" +
			"a single design sets --autofix=false, so an unattended fix never lands one of\n" +
			"its steps alone.",
	})
	goclikit.WithRecoveryHints(cmd, initiativeHints...)
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupReading,
		newInitiativesListCommand(),
		newInitiativesShowCommand(),
	)...)
	cmd.AddCommand(inGroup(groupChanging,
		newInitiativesCreateCommand(),
		newInitiativesEditCommand(),
		newInitiativesCompleteCommand(),
		newInitiativesDropCommand(),
		newInitiativesShelveCommand(),
		newInitiativesReopenCommand(),
		newInitiativesDeleteCommand(),
	)...)
	return cmd
}

func newInitiativesListCommand() *cobra.Command {
	var (
		asJSON           bool
		initiativeStatus string
		sort             string
		limit            int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the active initiatives in the order to work them",
		Long: "Active initiatives by priority, then position, under a header for each tier.\n" +
			"Someday, completed and dropped ones are hidden until --status asks for them,\n" +
			"and come after the active ones, finished ones latest first.\n\n" +
			"Each row leads with the initiative's number, which every initiative command\n" +
			"and --initiative flag accepts. The bar is its issues done over done and open.",
		Example: "  slate issues initiatives list\n  slate issues initiatives list --sort open\n  slate issues initiatives list --status all --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("status") && !slices.Contains(api.InitiativeStatuses, initiativeStatus) {
				return goclikit.UsageError(fmt.Errorf("unknown status %q — one of: %s", initiativeStatus, strings.Join(api.InitiativeStatuses, ", ")))
			}
			if cmd.Flags().Changed("sort") && !slices.Contains(api.InitiativeSorts, sort) {
				return goclikit.UsageError(fmt.Errorf("unknown sort %q — one of: %s", sort, strings.Join(api.InitiativeSorts, ", ")))
			}
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			initiatives, err := client.ListInitiatives(cmd.Context(), initiativeStatus, sort, limitFlag(cmd))
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), initiatives)
			}
			printInitiativesTable(cmd.OutOrStdout(), initiatives, sort)
			if !cmd.Flags().Changed("status") {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "\n"+initiativeHints[0])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&initiativeStatus, "status", "", "One of: "+strings.Join(api.InitiativeStatuses, ", ")+" (default active)")
	cmd.Flags().StringVar(&sort, "sort", "", "One of: "+strings.Join(api.InitiativeSorts, ", ")+" (default priority); open puts the most unclosed issues first")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output initiatives as JSON to stdout")
	addLimitFlag(cmd, &limit)
	return cmd
}

func newInitiativesShowCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "show <initiative>",
		Short:   "Show an initiative and its unclosed issues in queue order",
		Example: "  slate issues initiatives show 1501\n  slate issues initiatives show \"Ship the tracker\"",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			initiative, err := client.GetInitiative(cmd.Context(), args[0])
			if err != nil {
				return handleArgumentAPIError(err)
			}
			issues, err := client.ListIssues(cmd.Context(), api.IssueFilter{Initiative: initiative.ID}, api.Window{}, issueZone(), nil)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), struct {
					api.Initiative
					Issues []api.Issue `json:"issues"`
				}{initiative, issues})
			}
			printInitiativeDetail(cmd.OutOrStdout(), initiative)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nUnclosed issues (%d):\n", len(issues))
			if len(issues) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  (none)")
				return nil
			}
			printIssuesTable(cmd.OutOrStdout(), issues, time.Now())
			noteInheritedPriority(cmd, issues)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the initiative and its issues as JSON to stdout")
	return cmd
}

func newInitiativesCreateCommand() *cobra.Command {
	var (
		name        string
		description string
		priority    string
		position    int
		autofix     bool
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "create --name <name> [flags]",
		Short: "Start an initiative",
		Example: "  slate issues initiatives create --name \"Ship the tracker\" --priority high\n" +
			"  slate issues initiatives create --name \"Build day\" --autofix=false",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(name) == "" {
				return goclikit.UsageError(errors.New("--name is required"))
			}
			f := cmd.Flags()
			in := api.InitiativeCreateInput{Name: name, Description: valuedStrFlag(f, "description", &description)}
			if f.Changed("priority") {
				value, err := parsePriority(priority)
				if err != nil {
					return goclikit.UsageError(fmt.Errorf("--priority: %w", err))
				}
				in.Priority = &value
			}
			if f.Changed("position") {
				in.Position = &position
			}
			if f.Changed("autofix") {
				in.Autofix = &autofix
			}
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.CreateInitiative(cmd.Context(), in)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			initiative := written.Initiative
			return reportWrite(cmd, asJSON, written, written.WriteEffects, fmt.Sprintf("Started initiative #%d %q", initiative.Number, initiative.Name))
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Initiative name, unique among active ones (required)")
	cmd.Flags().StringVar(&description, "description", "", "The outcome, and how you will know it is done")
	cmd.Flags().StringVar(&priority, "priority", "", "One of: "+priorityChoices()+" (default none)")
	cmd.Flags().IntVar(&position, "position", 0, "Place among initiatives of its priority (default last)")
	cmd.Flags().BoolVar(&autofix, "autofix", true, initiativeAutofixHelp)
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newInitiativesEditCommand() *cobra.Command {
	var (
		name        string
		description string
		priority    string
		position    int
		autofix     bool
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "edit <initiative> [flags]",
		Short: "Change an initiative's fields",
		Long: "Update only the fields whose flags you pass. --description \"\" clears it.\n" +
			"Status moves through complete, drop, shelve and reopen.",
		Example: "  slate issues initiatives edit \"Ship the tracker\" --priority urgent\n" +
			"  slate issues initiatives edit 1491 --autofix=false",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			if readFlagIntent(f, "name") == flagClears {
				return goclikit.UsageError(errors.New("--name cannot be emptied — every initiative carries one"))
			}
			in := api.InitiativeUpdateInput{
				Name:        valuedStrFlag(f, "name", &name),
				Description: valuedStrFlag(f, "description", &description),
			}
			if f.Changed("priority") {
				value, err := parsePriority(priority)
				if err != nil {
					return goclikit.UsageError(fmt.Errorf("--priority: %w", err))
				}
				in.Priority = &value
			}
			if f.Changed("position") {
				in.Position = &position
			}
			if f.Changed("autofix") {
				in.Autofix = &autofix
			}
			var clear []string
			if readFlagIntent(f, "description") == flagClears {
				clear = append(clear, "description")
			}
			if in == (api.InitiativeUpdateInput{}) && len(clear) == 0 {
				return goclikit.UsageError(errors.New("nothing to change — pass at least one of --name/--description/--priority/--position/--autofix"))
			}
			return runInitiativeUpdate(cmd, args[0], in, clear, asJSON, "Updated")
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New name")
	cmd.Flags().StringVar(&description, "description", "", "New description (empty clears)")
	cmd.Flags().StringVar(&priority, "priority", "", "One of: "+priorityChoices())
	cmd.Flags().IntVar(&position, "position", 0, "New place among initiatives of its priority")
	cmd.Flags().BoolVar(&autofix, "autofix", true, initiativeAutofixHelp)
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

const initiativeAutofixHelp = "False keeps every issue in it from an unattended fix, so a session takes them"

func runInitiativeUpdate(cmd *cobra.Command, ref string, in api.InitiativeUpdateInput, clear []string, asJSON bool, verb string) error {
	client, err := newAPIClient(cmd.Context())
	if err != nil {
		return handleAPIError(err)
	}
	written, err := client.UpdateInitiative(cmd.Context(), ref, in, clear, issueZone())
	if err != nil {
		return handleArgumentAPIError(err)
	}
	initiative := written.Initiative
	summary := fmt.Sprintf("%s initiative #%d %q (%s)", verb, initiative.Number, initiative.Name, initiative.Status)
	return reportWrite(cmd, asJSON, written, written.WriteEffects, summary)
}

func newInitiativesCompleteCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "complete <initiative>",
		Short: "Mark an initiative's outcome reached, which hides it",
		Long: "Its issues keep their status, so one still open stays in the queue. It ends\n" +
			"the initiative's claim as completed, and prints whose claim it ended.",
		Example: "  slate issues initiatives complete \"Ship the tracker\"",
		Args:    cobra.ExactArgs(1),
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
		Example: "  slate issues initiatives drop \"Rewrite the router\" --reason \"Traefik covers it\"",
		Args:    cobra.ExactArgs(1),
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
		Long: "For an idea captured in full but not taken on. It moves to --status someday,\n" +
			"out of the active list, and no close is recorded. Its issues keep their\n" +
			"state, but `slate issues next` leaves them out, so no agent takes them. One\n" +
			"that work outside someday depends on stays in, at that work's priority.\n" +
			"It lends no priority, and gives up its name, so a new initiative may take\n" +
			"it. `slate issues initiatives reopen` makes its issues ready again.",
		Example: "  slate issues initiatives shelve \"Measure everything\"",
		Args:    cobra.ExactArgs(1),
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
		Use:     "reopen <initiative>",
		Short:   "Return a shelved or finished initiative to the active list",
		Long:    "Refused if an active initiative has taken the name in the meantime.",
		Example: "  slate issues initiatives reopen \"Ship the tracker\"",
		Args:    cobra.ExactArgs(1),
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
		Use:   "delete <initiative>",
		Short: "Delete an initiative and its claims, leaving its issues in none",
		Long: "Every claim it has had goes with it, live or ended, with a warning when\n" +
			"another holder's live claim was one. It prints each claim it took and each\n" +
			"issue it left in no initiative. Prompts unless --yes.",
		Example: "  slate issues initiatives delete \"Ship the tracker\" --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd.Context())
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

// progressCells is how many cells the bar of done over open-and-done spans.
const progressCells = 10

// progressFilled is how many of the bar's cells are filled. Any finished issue
// fills one, and any open issue keeps one empty, so the bar never reads as
// untouched or finished when it is not.
func progressFilled(done, open int) int {
	total := done + open
	if done == 0 || total == 0 {
		return 0
	}
	filled := (done*progressCells + total/2) / total
	ceiling := progressCells
	if open > 0 {
		ceiling--
	}
	return min(max(filled, 1), ceiling)
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
	filled := progressFilled(done, open)

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
	bar := p.Green(strings.Repeat("▰", filled)) + strings.Repeat("▱", progressCells-filled)
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
			_, _ = fmt.Fprintln(out, rows.row(initiative))
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
			_, _ = fmt.Fprintln(out, rows.row(initiative))
		}
		i = end
	}
}

func printInitiativeDetail(out io.Writer, initiative api.Initiative) {
	p := gotermstyle.PaletteFor(out)
	_, _ = fmt.Fprintf(out, "%s  %s\n", p.Cyan("#"+strconv.Itoa(initiative.Number)), p.Bold(initiative.Name))
	status := initiative.Status
	if initiative.ClosedTS != nil {
		status += " (" + localDay(*initiative.ClosedTS) + ")"
	}
	_, _ = fmt.Fprintf(out, "  status:    %s\n", status)
	if reason := strValue(initiative.StatusReason); reason != "" {
		_, _ = fmt.Fprintf(out, "  reason:    %s\n", reason)
	}
	_, _ = fmt.Fprintf(out, "  priority:  %s\n", tierColor(p, initiative.Priority)(priorityName(initiative.Priority)))
	if !initiative.Autofix {
		_, _ = fmt.Fprintf(out, "  autofix:   off\n")
	}
	_, _ = fmt.Fprintf(out, "  issues:    %d (%d open, %d done, %d canceled)\n",
		initiative.IssueCount, initiative.OpenCount, initiative.CompletedCount, initiative.CanceledCount)
	_, _ = fmt.Fprintf(out, "  repos:     %s\n", repoList(initiative.Repos))
	if initiative.Claim != nil {
		_, _ = fmt.Fprintf(out, "  claim:     %s\n", claimNote(*initiative.Claim, time.Now()))
	}
	if initiative.Idea != nil {
		_, _ = fmt.Fprintf(out, "  idea:      #%d %s\n", initiative.Idea.Number, initiative.Idea.Title)
	}
	if description := strValue(initiative.Description); description != "" {
		_, _ = fmt.Fprintf(out, "\nDescription:\n%s\n", indent(description, "  "))
	}
}
