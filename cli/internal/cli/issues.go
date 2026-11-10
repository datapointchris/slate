package cli

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/gotermstyle"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/datapointchris/slate/cli/internal/api"
)

// issueHints are the ways to find a valid issue number after a 404.
var issueHints = []string{
	anyRecordHint,
	searchHint,
	"Completed and canceled issues are hidden: slate issues list --status all",
}

// joinedHints is the hints of a command that reads more than one kind, each
// line once, because every kind's list opens with anyRecordHint.
func joinedHints(lists ...[]string) []string {
	var joined []string
	for _, hint := range slices.Concat(lists...) {
		if !slices.Contains(joined, hint) {
			joined = append(joined, hint)
		}
	}
	return joined
}

const (
	issueGroupRead      = "read"
	issueGroupFile      = "file"
	issueGroupLifecycle = "lifecycle"
	issueGroupOrder     = "order"
)

func newIssuesCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:        "issues",
		SuggestFor: []string{"items", "item"},
		Short:      "Track development work: bugs, features, chores, and the decisions they wait on",
		Long: "Development work, mostly filed and taken by agents. An issue needs no project:\n" +
			"its repo, labels and dependencies place it, and an optional initiative groups\n" +
			"the issues that ship together.\n" +
			"\n" +
			"An issue is ready when it is open, nobody holds a live claim on it,\n" +
			"everything it depends on is closed, its deferral day has come, none of its\n" +
			"children is still open, and it is not shelved in a someday initiative. A\n" +
			"someday issue that work outside someday depends on is not shelved.\n" +
			"`slate next` lists the ready issues in the order to take them: priority\n" +
			"first, each initiative's issues together, then rank. `issues reorder` moves an\n" +
			"issue. An issue sorts by the most urgent of its own priority, its\n" +
			"parent's, its initiative's, and that of anything it blocks. Its own priority\n" +
			"can raise it, never lower it. `slate dependencies` adds and draws what an\n" +
			"issue waits on.\n" +
			"\n" +
			"Working an issue starts with a claim and ends with a close. Before the first\n" +
			"edit, `slate claims hold <issue>` takes it, or `slate claims hold --next`\n" +
			"takes the head of `slate next`. A change that carries several issues holds\n" +
			"each one. `complete` and `cancel` end the claim and say so. `slate claims\n" +
			"release` hands an issue back unfinished, and `slate claims list` shows every\n" +
			"claim held.\n" +
			"\n" +
			"A decision waits on a person, so `slate next` lists it at step decide and\n" +
			"`slate claims hold --next` leaves it out. An open question the work waits on\n" +
			"is a decision issue it depends on.",
		Example: "  slate issues list --repo webapp                                  the open work in one repo\n" +
			"  slate issues show 412                                            one issue, its edges and comments\n" +
			"  slate issues create --title \"Routing drops /issues\" --type bug   file a bug\n" +
			"  slate issues complete 412                                        close it once the work lands",
	})
	goclikit.WithRecoveryHints(cmd, issueHints...)
	cmd.AddGroup(
		&cobra.Group{ID: issueGroupRead, Title: "Reading:"},
		&cobra.Group{ID: issueGroupFile, Title: "Filing:"},
		&cobra.Group{ID: issueGroupLifecycle, Title: "Lifecycle:"},
		&cobra.Group{ID: issueGroupOrder, Title: "Order:"},
	)
	add := func(group string, commands ...*cobra.Command) {
		for _, c := range commands {
			c.GroupID = group
			cmd.AddCommand(c)
		}
	}
	add(issueGroupRead,
		newIssuesListCommand(),
		goclikit.WithRecoveryHints(newIssuesShowCommand(), issueHints...),
	)
	add(issueGroupFile,
		goclikit.WithRecoveryHints(newIssuesCreateCommand(), joinedHints(issueHints, initiativeHints)...),
		goclikit.WithRecoveryHints(newIssuesEditCommand(), joinedHints(issueHints, initiativeHints)...),
		goclikit.WithRecoveryHints(newIssuesDeleteCommand(), issueHints...),
	)
	add(issueGroupLifecycle,
		goclikit.WithRecoveryHints(newIssuesAcceptCommand(), issueHints...),
		goclikit.WithRecoveryHints(newIssuesCompleteCommand(), issueHints...),
		goclikit.WithRecoveryHints(newIssuesCancelCommand(), issueHints...),
		goclikit.WithRecoveryHints(newIssuesReopenCommand(), issueHints...),
	)
	add(issueGroupOrder,
		goclikit.WithRecoveryHints(newIssuesReorderCommand(), issueHints...),
		goclikit.WithRecoveryHints(newIssuesPinCommand(), issueHints...),
		goclikit.WithRecoveryHints(newIssuesUnpinCommand(), issueHints...),
	)
	return cmd
}

// --- Filters ---

// issueFilterFlags are the questions a list of issues answers.
type issueFilterFlags struct {
	status      string
	repo        string
	issueType   string
	label       string
	tag         string
	initiative  string
	parent      string
	priority    string
	closedBy    string
	search      string
	blocked     bool
	autofixable bool
}

func (v *issueFilterFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&v.status, "status", "", "One of: "+strings.Join(api.IssueStatuses, ", ")+" (default unclosed)")
	withFlagValues(cmd, "status", api.IssueStatuses...)
	f.StringVar(&v.repo, "repo", "", "Only issues on this repo (empty string for issues on no repo)")
	f.StringVar(&v.issueType, "type", "", "One of: "+strings.Join(api.IssueTypes, ", "))
	withFlagValues(cmd, "type", api.IssueTypes...)
	f.StringVar(&v.label, "label", "", "Only issues carrying this label (see: slate labels list)")
	f.StringVar(&v.tag, "tag", "", tagFilterHelp)
	f.StringVar(&v.initiative, "initiative", "", "Only issues in this initiative, by number, name or id")
	f.StringVar(&v.parent, "parent", "", "Only the children of this issue")
	f.StringVar(&v.priority, "priority", "", "Only issues whose own priority is this: "+priorityChoices())
	withFlagValues(cmd, "priority", api.IssuePriorityNames...)
	f.StringVar(&v.closedBy, "closed-by", "", "Only issues whose closer starts with this, such as claude-code/")
	f.StringVar(&v.search, "search", "", "Only issues whose title or description holds every one of these words, or whose number one names")
	f.BoolVar(&v.blocked, "blocked", false, "Only issues waiting on an unclosed dependency (--blocked=false for the rest)")
	f.BoolVar(&v.autofixable, "autofixable", false, autofixableFilterHelp)
}

const autofixableFilterHelp = "Only issues whose own, initiative's and parents' autofix are all on (--autofixable=false for the rest)"

// filter validates what can be refused without the API and builds the query.
func (v *issueFilterFlags) filter(cmd *cobra.Command) (api.IssueFilter, error) {
	f := cmd.Flags()
	if f.Changed("status") && !slices.Contains(api.IssueStatuses, v.status) {
		return api.IssueFilter{}, goclikit.UsageError(fmt.Errorf("unknown status %q — one of: %s", v.status, strings.Join(api.IssueStatuses, ", ")))
	}
	if err := validateIssueType(f, "type", v.issueType); err != nil {
		return api.IssueFilter{}, err
	}
	repo, err := repoFlagValue(cmd, v.repo)
	if err != nil {
		return api.IssueFilter{}, err
	}
	filter := api.IssueFilter{
		Status:     v.status,
		Repo:       repo,
		Type:       v.issueType,
		Label:      v.label,
		Tag:        v.tag,
		Initiative: v.initiative,
		Parent:     v.parent,
		ClosedBy:   v.closedBy,
		Search:     v.search,
	}
	if f.Changed("priority") {
		priority, err := parsePriority(v.priority)
		if err != nil {
			return api.IssueFilter{}, goclikit.UsageError(fmt.Errorf("--priority: %w", err))
		}
		filter.Priority = &priority
	}
	if f.Changed("blocked") {
		filter.Blocked = &v.blocked
	}
	if f.Changed("autofixable") {
		filter.Autofixable = &v.autofixable
	}
	return filter, nil
}

// honoredAutofixable refuses a reply an API that predates the filter answered
// unfiltered, which would hand an unattended fix the work kept from it.
func honoredAutofixable(want *bool, issues []api.Issue) error {
	if want == nil {
		return nil
	}
	for _, issue := range issues {
		if issue.IsAutofixable == nil || *issue.IsAutofixable != *want {
			return fmt.Errorf("the API ignored --autofixable: #%d came back without a matching is_autofixable, so it predates the filter", issue.Number)
		}
	}
	return nil
}

func validateIssueType(f *pflag.FlagSet, name, value string) error {
	if !f.Changed(name) || slices.Contains(api.IssueTypes, value) {
		return nil
	}
	return goclikit.UsageError(fmt.Errorf("unknown type %q — one of: %s", value, strings.Join(api.IssueTypes, ", ")))
}

// parsePriority reads a priority by name or by value, so `--priority high` and
// `--priority 2` are the same filter.
func parsePriority(raw string) (int, error) {
	if n, err := strconv.Atoi(raw); err == nil && n >= 0 && n < len(api.IssuePriorityNames) {
		return n, nil
	}
	for value, name := range api.IssuePriorityNames {
		if strings.EqualFold(strings.TrimSpace(raw), name) {
			return value, nil
		}
	}
	return 0, fmt.Errorf("unknown priority %q — one of: %s", raw, priorityChoices())
}

func priorityChoices() string {
	return strings.Join(api.IssuePriorityNames, ", ") + " (or 0-4)"
}

func priorityName(value int) string {
	if value < 0 || value >= len(api.IssuePriorityNames) {
		return strconv.Itoa(value)
	}
	return api.IssuePriorityNames[value]
}

// issueZone is the calendar a deferral day is read in, which is this machine's.
func issueZone() api.DayZone { return api.DayZone(LocalZoneName()) }

// --- Reads over collections ---

func newIssuesListCommand() *cobra.Command {
	var (
		asJSON  bool
		filters issueFilterFlags
		limit   int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List issues, open work first in queue order",
		Long: "Unclosed issues by default: triage and open. Completed and canceled ones\n" +
			"accumulate without end, so --status asks for them. --search keeps the issues\n" +
			"whose title or description holds every word, beside any other filter. A word\n" +
			"that is a number, as 2175 or #2175, also matches that issue.\n" +
			"`slate search <words>` finds a record of any kind.\n" +
			"\n" +
			"Open work comes first in the order `slate next` takes it, then closed work,\n" +
			"latest first. The priority column is the one the queue sorts by, and a * marks\n" +
			"one inherited from what the issue blocks, its parent, or its initiative.\n" +
			"--priority filters on an issue's own.\n" +
			"\n" +
			"--since/--until bound when an issue closed. Pair them with --status\n" +
			"completed to read a week's finished work.\n" +
			"\n" +
			"--closed-by matches the start of who closed an issue, so claude-code/ is\n" +
			"every attended Claude Code session. docs/issues.md lists the kinds.",
		Example: "  slate issues list\n" +
			"  slate issues list --repo webapp\n" +
			"  slate issues list --search routing --repo webapp --status all\n" +
			"  slate issues list --type decision\n" +
			"  slate issues list --status triage\n" +
			"  slate issues list --blocked\n" +
			"  slate issues list --label area-cli --priority high\n" +
			"  slate issues list --status completed --since 2026-10-01 --json\n" +
			"  slate issues list --status completed --since 7d\n" +
			"  slate issues list --status completed --closed-by claude-code/",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter, err := filters.filter(cmd)
			if err != nil {
				return err
			}
			window, err := readWindow(cmd)
			if err != nil {
				return err
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			issues, err := client.ListIssues(cmd.Context(), filter, window, issueZone(), limitFlag(cmd))
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if err := honoredAutofixable(filter.Autofixable, issues); err != nil {
				return err
			}
			if err := honoredTagFilter(filter.Tag, issues, issueTags); err != nil {
				return err
			}
			if !cmd.Flags().Changed("status") {
				defer noteHiddenByStatus(cmd, "Completed and canceled issues")
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), issues)
			}
			printIssuesTable(cmd.OutOrStdout(), issues, time.Now())
			noteInheritedPriority(cmd, issues)
			return nil
		},
	}
	filters.register(cmd)
	addWindowFlags(cmd, "issues closed")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output issues as JSON to stdout")
	addLimitFlag(cmd, &limit)
	return cmd
}

// --- Reads of one issue ---

func newIssuesShowCommand() *cobra.Command {
	var (
		asJSON      bool
		changeCount int
	)
	cmd := &cobra.Command{
		Use:         "show <issue>",
		Short:       "Show an issue with its edges, children, documents, comments and changes",
		Example:     "  slate issues show 412\n  slate issues show 412 --json --changes 100",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			issue, err := client.GetIssue(cmd.Context(), args[0], issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			changes, err := readChanges(cmd, client, issue.Number, changeCount, asJSON)
			if err != nil {
				return err
			}
			defer noteChanges(cmd, changes)
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), issueShown{IssueDetail: issue, Changes: changes})
			}
			out, now := cmd.OutOrStdout(), time.Now()
			printIssueDetail(out, issue, now)
			printChanges(out, changes, now)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the issue as JSON to stdout")
	addChangesFlag(cmd, &changeCount)
	return cmd
}

// issueShown is what every show of an issue prints as JSON: the issue, with
// the changes the show read beside its own fields.
type issueShown struct {
	api.IssueDetail
	Changes *recordChanges `json:"changes,omitempty"`
}

// --- Rendering ---

func printIssuesTable(out io.Writer, issues []api.Issue, now time.Time) {
	if len(issues) == 0 {
		_, _ = fmt.Fprintln(out, "No issues.")
		return
	}
	p := gotermstyle.PaletteFor(out)
	header := []string{"#", "PRIORITY", "TYPE", "STATUS", "REPO"}
	rows := make([][]string, len(issues))
	for i, issue := range issues {
		rows[i] = []string{strconv.Itoa(issue.Number), priorityCell(issue), issue.Type, issue.Status, orDash(strValue(issue.Repo))}
	}
	widths := make([]int, len(header))
	for c := range header {
		column := []string{header[c]}
		for _, row := range rows {
			column = append(column, row[c])
		}
		widths[c] = widest(column)
	}

	line := make([]string, 0, len(header)+1)
	for c, title := range header {
		line = append(line, paint(p.Bold, title, widths[c]))
	}
	_, _ = fmt.Fprintln(out, strings.Join(append(line, p.Bold("TITLE")), "  "))
	plain := func(text string) string { return text }
	for i, issue := range issues {
		colors := []func(string) string{p.Cyan, tierColor(p, issue.EffectivePriority), plain, plain, plain}
		line = line[:0]
		for c, cell := range rows[i] {
			line = append(line, paint(colors[c], cell, widths[c]))
		}
		title := issue.Title
		if notes := issueStateNotes(issue, now); len(notes) > 0 {
			title += "  [" + strings.Join(notes, "; ") + "]"
		}
		_, _ = fmt.Fprintln(out, strings.Join(append(line, title), "  "))
	}
}

// priorityCell shows the priority the queue sorts by, starred when inherited.
func priorityCell(issue api.Issue) string {
	name := priorityName(issue.EffectivePriority)
	if isPriorityInherited(issue) {
		return name + "*"
	}
	return name
}

func isPriorityInherited(issue api.Issue) bool {
	return issue.EffectivePriority != issue.Priority
}

// noteInheritedPriority explains the table's * on stderr, when a row carries one.
func noteInheritedPriority(cmd *cobra.Command, issues []api.Issue) {
	if !slices.ContainsFunc(issues, isPriorityInherited) {
		return
	}
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "\n* priority inherited from what the issue blocks, its parent, or its initiative")
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func isClosedIssue(issue api.Issue) bool {
	return issue.Status == api.IssueStatusCompleted || issue.Status == api.IssueStatusCanceled
}

// issueStateNotes says why an unclosed issue is or is not ready, in the words a
// reader acts on. A closed issue needs none: its status says it.
func issueStateNotes(issue api.Issue, now time.Time) []string {
	if isClosedIssue(issue) {
		return nil
	}
	var notes []string
	if issue.Claim != nil {
		notes = append(notes, claimNote(*issue.Claim, now))
	}
	if held := blockers(issue); held != "" {
		notes = append(notes, "blocked by "+held)
	}
	if day := strValue(issue.DeferredUntilDate); day != "" && day > now.Local().Format(dayLayout) {
		notes = append(notes, "deferred until "+day)
	}
	if issue.OpenChildCount > 0 {
		notes = append(notes, fmt.Sprintf("%d open %s", issue.OpenChildCount, plural(issue.OpenChildCount, "child", "children")))
	}
	return notes
}

// closedNote says when the issue closed and who closed it, where each is known.
func closedNote(issue api.Issue, now time.Time) string {
	if issue.ClosedTS == nil {
		return ""
	}
	when := localClock(*issue.ClosedTS, now)
	if by := strValue(issue.ClosedBy); by != "" {
		return when + " by " + by
	}
	return when
}

// localClock prints an instant as a time of day when it falls today here, and
// with its day otherwise.
func localClock(t, now time.Time) string {
	local := t.Local()
	if local.Format(dayLayout) == now.Local().Format(dayLayout) {
		return local.Format("15:04")
	}
	return local.Format("2006-01-02 15:04")
}

// openRecordNumbers names each initiative or issue in summaries that is still
// open, so a wait on it holds.

// blockers names what holds an unclosed issue back: each open issue it depends
// on, then each open record its initiative waits on. Empty means nothing does.
// blockers words what the API says holds an issue back: what it depends on,
// then what its initiative's wait names.
func blockers(issue api.Issue) string {
	var direct, waits []string
	var initiative int
	for _, blocker := range issue.BlockedBy {
		number := "#" + strconv.Itoa(blocker.Number)
		if blocker.ThroughInitiative == nil {
			direct = append(direct, number)
		} else {
			waits = append(waits, number)
			initiative = *blocker.ThroughInitiative
		}
	}
	var parts []string
	if len(direct) > 0 {
		parts = append(parts, strings.Join(direct, ", "))
	}
	if len(waits) > 0 {
		parts = append(parts, fmt.Sprintf("%s through initiative #%d", strings.Join(waits, ", "), initiative))
	}
	return strings.Join(parts, "; ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func summaryLine(summary api.IssueSummary) string {
	return fmt.Sprintf("%s %d %s", summaryMark(summary.Status), summary.Number, summary.Title)
}

// recordSummaryLine is summaryLine for a record of either kind, an initiative
// named as one.
func recordSummaryLine(summary api.RecordSummary) string {
	kind := ""
	if summary.Kind == api.RecordKindInitiative {
		kind = "initiative "
	}
	return fmt.Sprintf("%s %d %s%s", summaryMark(strValue(summary.Status)), summary.Number, kind, summary.Title)
}

func summaryMark(status string) string {
	switch status {
	case api.IssueStatusCompleted:
		return markCompleted
	case api.IssueStatusCanceled, api.InitiativeStatusDropped:
		return markArchived
	default:
		return markOpen
	}
}

func printIssueDetail(out io.Writer, issue api.IssueDetail, now time.Time) {
	_, _ = fmt.Fprintf(out, "#%d %s\n", issue.Number, issue.Title)
	field := func(label, value string) {
		if value != "" {
			_, _ = fmt.Fprintf(out, "  %-12s %s\n", label+":", value)
		}
	}
	field("type", issue.Type)
	status := issue.Status
	if notes := issueStateNotes(issue.Issue, now); len(notes) > 0 {
		status += " — " + strings.Join(notes, "; ")
	} else if issue.IsReady {
		status += " — ready"
	}
	field("status", status)
	field("closed", closedNote(issue.Issue, now))
	field("reason", strValue(issue.StatusReason))
	if issue.DuplicateOf != nil {
		field("duplicate of", summaryLine(*issue.DuplicateOf))
	}
	priority := priorityName(issue.EffectivePriority)
	if issue.EffectivePriority != issue.Priority {
		priority += " (inherited; its own is " + priorityName(issue.Priority) + ")"
	}
	field("priority", priority)
	field("repo", orDash(strValue(issue.Repo)))
	if issue.Initiative != nil {
		field("initiative", fmt.Sprintf("%d %s", issue.Initiative.Number, issue.Initiative.Name))
	}
	if issue.IsAutofixable != nil && !*issue.IsAutofixable && !isClosedIssue(issue.Issue) {
		field("autofix", "off")
	}
	if issue.Parent != nil {
		field("parent", summaryLine(*issue.Parent))
	}
	if issue.DiscoveredFrom != nil {
		field("found in", summaryLine(*issue.DiscoveredFrom))
	}
	field("labels", strings.Join(issue.Labels, ", "))
	field("tags", issueTagsLine(issue.Issue))
	field("paths", strings.Join(issue.Paths, ", "))
	field("deferred", strValue(issue.DeferredUntilDate))
	field("created", localDay(issue.CreatedTS))

	section := func(title, body string) {
		if body != "" {
			_, _ = fmt.Fprintf(out, "\n%s:\n%s\n", title, indent(body, "  "))
		}
	}
	section("Description", strValue(issue.Description))
	section("Acceptance", strValue(issue.Acceptance))

	edges := func(title string, summaries []api.IssueSummary) {
		if len(summaries) == 0 {
			return
		}
		_, _ = fmt.Fprintf(out, "\n%s (%d):\n", title, len(summaries))
		for _, summary := range summaries {
			_, _ = fmt.Fprintf(out, "  %s\n", summaryLine(summary))
		}
	}
	edges("Depends on", issue.DependsOn)
	edges("Blocks", issue.Blocks)
	edges("Children", issue.Children)
	records := func(title string, summaries []api.RecordSummary) {
		if len(summaries) == 0 {
			return
		}
		_, _ = fmt.Fprintf(out, "\n%s (%d):\n", title, len(summaries))
		for _, summary := range summaries {
			_, _ = fmt.Fprintf(out, "  %s\n", recordSummaryLine(summary))
		}
	}
	if issue.Initiative != nil {
		records(fmt.Sprintf("Initiative #%d waits on", issue.Initiative.Number), issue.InitiativeWaitsOn)
	}
	records("Initiatives waiting on it", issue.WaitingInitiatives)

	printRecordParts(out, issue.RecordParts, now)
}

func indent(text, prefix string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}
