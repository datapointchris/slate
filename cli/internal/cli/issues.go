package cli

import (
	"fmt"
	"io"
	"os"
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
	"Search issues by title or description: slate issues search <query>",
	"Completed and canceled issues are hidden: slate issues list --status all",
}

// defaultNextIssueLimit caps `next`. The head of the queue is the answer, and
// the rows after it are context.
const defaultNextIssueLimit = 10

const (
	issueGroupRead      = "read"
	issueGroupWork      = "work"
	issueGroupFile      = "file"
	issueGroupLifecycle = "lifecycle"
	issueGroupOrder     = "order"
	issueGroupGrouping  = "grouping"
)

func newIssuesCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use: "issues",
		// Top-level words that mean an issue: `items`, or a verb typed with no noun.
		SuggestFor: []string{"items", "show", "search"},
		Short:      "Track development work: bugs, features, chores, and the decisions they wait on",
		Long: "Development work, mostly filed and taken by agents. An issue needs no project:\n" +
			"its repo, labels and dependencies place it, and an optional initiative groups\n" +
			"the issues that ship together.\n" +
			"\n" +
			"An issue is ready when it is open or its claim has run out, everything it\n" +
			"depends on is closed, its deferral day has come, none of its children is\n" +
			"still open, and it is not shelved in a someday initiative. A someday issue\n" +
			"that work outside someday depends on is not shelved. `next` lists the ready\n" +
			"issues in the order to take them: priority first, then rank. An issue sorts\n" +
			"by the most urgent of its own priority, its parent's, its initiative's, and\n" +
			"that of anything it blocks. Its own priority can raise it, never lower it.\n" +
			"\n" +
			"Working an issue starts with a claim and ends with a close. Before the\n" +
			"first edit, `claim <issue>` takes it, or `claim` alone takes the head of\n" +
			"`next`. A change that carries several issues claims each one. A claimed\n" +
			"issue is in progress and `next` leaves it out, so no other session takes\n" +
			"it. `complete` lets go of the claim, `release` hands the issue back\n" +
			"unfinished, and claiming it again extends the claim. `list --status\n" +
			"in_progress` shows every claim held.\n" +
			"\n" +
			"Decisions wait on a person, so the queue leaves them out.\n" +
			"`slate issues list --type decision` lists them.",
	})
	goclikit.WithRecoveryHints(cmd, issueHints...)
	cmd.AddGroup(
		&cobra.Group{ID: issueGroupRead, Title: "Reading:"},
		&cobra.Group{ID: issueGroupWork, Title: "Taking work:"},
		&cobra.Group{ID: issueGroupFile, Title: "Filing:"},
		&cobra.Group{ID: issueGroupLifecycle, Title: "Lifecycle:"},
		&cobra.Group{ID: issueGroupOrder, Title: "Order and dependencies:"},
		&cobra.Group{ID: issueGroupGrouping, Title: "Grouping:"},
	)
	add := func(group string, commands ...*cobra.Command) {
		for _, c := range commands {
			c.GroupID = group
			cmd.AddCommand(c)
		}
	}
	add(issueGroupRead,
		newIssuesListCommand(),
		newIssuesSearchCommand(),
		goclikit.WithRecoveryHints(newIssuesShowCommand(), issueHints...),
		goclikit.WithRecoveryHints(newIssuesCommentsCommand(), issueHints...),
		goclikit.WithRecoveryHints(newIssuesTreeCommand(), issueHints...),
		newIssuesVocabularyCommand(),
	)
	add(issueGroupWork,
		newIssuesNextCommand(),
		goclikit.WithRecoveryHints(newIssuesClaimCommand(), issueHints...),
		goclikit.WithRecoveryHints(newIssuesReleaseCommand(), issueHints...),
	)
	add(issueGroupFile,
		goclikit.WithRecoveryHints(newIssuesCreateCommand(), append(slices.Clone(issueHints), initiativeHints...)...),
		goclikit.WithRecoveryHints(newIssuesEditCommand(), append(slices.Clone(issueHints), initiativeHints...)...),
		goclikit.WithRecoveryHints(newIssuesAddCommentCommand(), issueHints...),
		goclikit.WithRecoveryHints(newIssuesRemoveCommentCommand(), issueHints...),
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
		goclikit.WithRecoveryHints(newIssuesAddDependencyCommand(), issueHints...),
		goclikit.WithRecoveryHints(newIssuesRemoveDependencyCommand(), issueHints...),
	)
	add(issueGroupGrouping,
		newIssueInitiativesCommand(),
		newIssueLabelsCommand(),
	)
	return cmd
}

// --- Filters shared by list and search ---

// issueFilterFlags are the questions a list of issues answers, declared once
// so list and search ask them the same way.
type issueFilterFlags struct {
	status      string
	repo        string
	issueType   string
	label       string
	initiative  string
	parent      string
	priority    string
	claimedBy   string
	closedBy    string
	blocked     bool
	autofixable bool
}

func (v *issueFilterFlags) register(cmd *cobra.Command, statusDefault string) {
	f := cmd.Flags()
	f.StringVar(&v.status, "status", "", "One of: "+strings.Join(api.IssueStatuses, ", ")+" (default "+statusDefault+")")
	f.StringVar(&v.repo, "repo", "", "Only issues on this repo (empty string for issues on no repo)")
	f.StringVar(&v.issueType, "type", "", "One of: "+strings.Join(api.IssueTypes, ", "))
	f.StringVar(&v.label, "label", "", "Only issues carrying this label (see: slate issues labels list)")
	f.StringVar(&v.initiative, "initiative", "", "Only issues in this initiative, by number, name or id")
	f.StringVar(&v.parent, "parent", "", "Only the children of this issue")
	f.StringVar(&v.priority, "priority", "", "Only issues whose own priority is this: "+priorityChoices())
	f.StringVar(&v.claimedBy, "claimed-by", "", "Only issues held by this claimant")
	f.StringVar(&v.closedBy, "closed-by", "", "Only issues whose closer starts with this, such as claude-code/")
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
		Initiative: v.initiative,
		Parent:     v.parent,
		ClaimedBy:  v.claimedBy,
		ClosedBy:   v.closedBy,
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
		Long: "Unclosed issues by default: triage, open and in progress. Completed and\n" +
			"canceled ones accumulate without end, so --status asks for them.\n" +
			"\n" +
			"Open work comes first in the order `next` takes it, then closed work, latest\n" +
			"first. The priority column is the one the queue sorts by, and a * marks one\n" +
			"inherited from what the issue blocks, its parent, or its initiative. --priority\n" +
			"filters on an issue's own.\n" +
			"\n" +
			"--since/--until bound when an issue closed. Pair them with --status\n" +
			"completed to read a week's finished work.\n" +
			"\n" +
			"--closed-by matches the start of who closed an issue, so claude-code/ is\n" +
			"every attended Claude Code session. docs/issues.md lists the kinds.",
		Example: "  slate issues list\n" +
			"  slate issues list --repo webapp\n" +
			"  slate issues list --type decision\n" +
			"  slate issues list --status triage\n" +
			"  slate issues list --blocked\n" +
			"  slate issues list --label area-cli --priority high\n" +
			"  slate issues list --status completed --since 2026-10-01 --json\n" +
			"  slate issues list --status completed --since 7d\n" +
			"  slate issues list --status completed --closed-by claude-code/",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter, err := filters.filter(cmd)
			if err != nil {
				return err
			}
			window, err := readWindow(cmd)
			if err != nil {
				return err
			}
			hidClosed := !cmd.Flags().Changed("status")
			return runIssuesCollection(cmd, asJSON, hidClosed, func(c *api.Client) ([]api.Issue, error) {
				issues, err := c.ListIssues(cmd.Context(), filter, window, issueZone(), limitFlag(cmd))
				if err != nil {
					return nil, err
				}
				return issues, honoredAutofixable(filter.Autofixable, issues)
			})
		},
	}
	filters.register(cmd, "unclosed")
	addWindowFlags(cmd, "issues closed")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output issues as JSON to stdout")
	addLimitFlag(cmd, &limit)
	return cmd
}

func newIssuesSearchCommand() *cobra.Command {
	var (
		asJSON  bool
		filters issueFilterFlags
		limit   int
	)
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Find issues by title or description, closed ones included",
		Long: "Every word of the query must appear in the title or the description, in any\n" +
			"order. Searches every status unless --status narrows it, because the issue you\n" +
			"remember may be the one that was finished or canceled.",
		Example: "  slate issues search \"rank renumber\"\n" +
			"  slate issues search routing --repo webapp --status open",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filter, err := filters.filter(cmd)
			if err != nil {
				return err
			}
			if filter.Status == "" {
				filter.Status = api.IssueStatusAll
			}
			filter.Search = args[0]
			return runIssuesCollection(cmd, asJSON, false, func(c *api.Client) ([]api.Issue, error) {
				issues, err := c.ListIssues(cmd.Context(), filter, api.Window{}, issueZone(), limitFlag(cmd))
				if err != nil {
					return nil, err
				}
				return issues, honoredAutofixable(filter.Autofixable, issues)
			})
		},
	}
	filters.register(cmd, "all")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output issues as JSON to stdout")
	addLimitFlag(cmd, &limit)
	return cmd
}

// runIssuesCollection renders a read returning issues as JSON or a table. When
// hidClosed, the read took the default that leaves closed issues out, and says
// so on stderr with the command that widens it.
func runIssuesCollection(cmd *cobra.Command, asJSON, hidClosed bool, read func(*api.Client) ([]api.Issue, error)) error {
	client, err := newAPIClient(cmd.Context())
	if err != nil {
		return handleAPIError(err)
	}
	issues, err := read(client)
	if err != nil {
		return handleArgumentAPIError(err)
	}
	if asJSON {
		return encodeJSON(cmd.OutOrStdout(), issues)
	}
	printIssuesTable(cmd.OutOrStdout(), issues, time.Now())
	noteInheritedPriority(cmd, issues)
	if hidClosed {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nCompleted and canceled issues are hidden: %s\n", reinvocation(cmd, "--status all"))
	}
	return nil
}

// reinvocation is the command that was run, flags and all, with extra added: a
// hint that widens a filtered read must keep the filters it widens.
func reinvocation(cmd *cobra.Command, extra string) string {
	parts := []string{cmd.CommandPath()}
	for _, arg := range cmd.Flags().Args() {
		parts = append(parts, shellQuote(arg))
	}
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		if flag.Name == "json" {
			return
		}
		if flag.Value.Type() == "bool" {
			if flag.Value.String() == "true" {
				parts = append(parts, "--"+flag.Name)
			} else {
				parts = append(parts, "--"+flag.Name+"=false")
			}
			return
		}
		for _, value := range rawFlagValues(flag) {
			parts = append(parts, "--"+flag.Name, shellQuote(value))
		}
	})
	return strings.Join(append(parts, extra), " ")
}

func newIssuesNextCommand() *cobra.Command {
	var (
		asJSON bool
		queue  readyQueueFlags
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "next",
		Short: "The ready issues, in the order to take them",
		Long: "Open, unblocked, not deferred past today, with no open children, and not held\n" +
			"by a live claim. The first row is what `slate issues claim` with no issue would\n" +
			"take, under the same flags. Decisions wait on a person and are left out unless\n" +
			"--type decision asks for them. --autofixable keeps only the issues whose own,\n" +
			"initiative's and parents' autofix are all on, the queue an unattended fix reads.\n" +
			"Reading the queue holds nothing, so claim the issue you take before working it.\n" +
			"\n" +
			"The priority column is the one the queue sorts by, and a * marks one inherited\n" +
			"from what the issue blocks, its parent, or its initiative.",
		Example: "  slate issues next\n" +
			"  slate issues next --repo webapp --limit 1\n" +
			"  slate issues next --label area-cli --json\n" +
			"  slate issues next --autofixable --json\n" +
			"  slate issues next --type decision",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter, err := queue.filter(cmd)
			if err != nil {
				return err
			}
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			// Uncapped, so an empty queue is told apart from --limit 0 below.
			ready, err := client.ListReadyIssues(cmd.Context(), filter, issueZone(), nil)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if err := honoredAutofixable(filter.Autofixable, ready); err != nil {
				return err
			}
			shown := ready[:min(limit, len(ready))]
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), shown)
			}
			if len(ready) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No issue is ready.")
				return nil
			}
			if len(shown) > 0 {
				printIssuesTable(cmd.OutOrStdout(), shown, time.Now())
			}
			noteInheritedPriority(cmd, shown)
			noteCappedRows(cmd, len(shown), len(ready))
			return nil
		},
	}
	queue.register(cmd)
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output issues as JSON to stdout")
	addCapFlag(cmd, &limit, defaultNextIssueLimit, "Maximum number of issues to show")
	return cmd
}

// readyQueueFlags narrow the ready queue, on `next` and on `claim` with no issue.
type readyQueueFlags struct {
	repo        string
	issueType   string
	label       string
	initiative  string
	autofixable bool
}

// readyQueueFlagNames are refused by a claim that names its issue.
func readyQueueFlagNames() []string {
	probe := &cobra.Command{}
	new(readyQueueFlags).register(probe)
	var names []string
	probe.Flags().VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	return names
}

func (v *readyQueueFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&v.repo, "repo", "", "Only issues on this repo (empty string for issues on no repo)")
	f.StringVar(&v.issueType, "type", "", "Only this type: "+strings.Join(api.IssueTypes, ", ")+" (decisions only when named)")
	f.StringVar(&v.label, "label", "", "Only issues carrying this label")
	f.StringVar(&v.initiative, "initiative", "", "Only issues in this initiative, by number, name or id")
	f.BoolVar(&v.autofixable, "autofixable", false, autofixableFilterHelp)
}

func (v *readyQueueFlags) filter(cmd *cobra.Command) (api.ReadyFilter, error) {
	if err := validateIssueType(cmd.Flags(), "type", v.issueType); err != nil {
		return api.ReadyFilter{}, err
	}
	repo, err := repoFlagValue(cmd, v.repo)
	if err != nil {
		return api.ReadyFilter{}, err
	}
	filter := api.ReadyFilter{
		Repo:       repo,
		Type:       v.issueType,
		Label:      v.label,
		Initiative: v.initiative,
	}
	if cmd.Flags().Changed("autofixable") {
		filter.Autofixable = &v.autofixable
	}
	return filter, nil
}

// --- Reads of one issue ---

func newIssuesShowCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "show <issue>",
		Short:   "Show an issue with its edges, children and comments",
		Example: "  slate issues show 412\n  slate issues show 412 --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			issue, err := client.GetIssue(cmd.Context(), args[0], issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), issue)
			}
			printIssueDetail(cmd.OutOrStdout(), issue, time.Now())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the issue as JSON to stdout")
	return cmd
}

func newIssuesCommentsCommand() *cobra.Command {
	var (
		asJSON bool
		limit  int
	)
	cmd := &cobra.Command{
		Use:     "comments <issue>",
		Short:   "List an issue's comments, oldest first",
		Long:    "Each row's #n is its place in the thread, which `remove-comment` takes.",
		Example: "  slate issues comments 412\n  slate issues comments 412 --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			comments, err := client.ListIssueComments(cmd.Context(), args[0], limitFlag(cmd))
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), comments)
			}
			if len(comments) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No comments.")
				return nil
			}
			printIssueComments(cmd.OutOrStdout(), comments)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output comments as JSON to stdout")
	addLimitFlag(cmd, &limit)
	return cmd
}

func newIssuesVocabularyCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "vocabulary",
		Short:   "List the values --status, --type, --priority and --label accept",
		Example: "  slate issues vocabulary\n  slate issues vocabulary --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			vocabulary, err := client.GetIssueVocabulary(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), vocabulary)
			}
			printIssueVocabulary(cmd.OutOrStdout(), vocabulary)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the vocabulary as JSON to stdout")
	return cmd
}

// --- Identity ---

// actorWorkerEnv names the variable a scheduler sets to say which of its
// workers is running, so what the worker does is signed with that name.
const actorWorkerEnv = "SLATE_ACTOR"

// actorIdentity names who acts when a claim, a comment or a close does not say.
// Two sessions on one machine must not share a name, because a claimant
// extends its own claim. The prefix is the kind of actor, so a reader can
// filter on it. docs/issues.md lists the kinds.
func actorIdentity() string {
	worker := os.Getenv(actorWorkerEnv)
	if session := os.Getenv("CLAUDE_CODE_SESSION_ID"); session != "" {
		switch {
		case worker != "":
			return "claude-code-unattended/" + worker + "/" + session
		case os.Getenv("CLAUDE_CODE_SESSION_ATTENDED") == "0":
			return "claude-code-unattended/" + session
		default:
			return "claude-code/" + session
		}
	}
	if worker != "" {
		return "worker/" + worker
	}
	user := os.Getenv("USER")
	if user == "" {
		user = "unknown"
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		return user
	}
	return user + "@" + host
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
	if claim := claimNote(issue, now); claim != "" {
		notes = append(notes, claim)
	}
	if blockers := openNumbers(issue.DependsOn); len(blockers) > 0 {
		notes = append(notes, "blocked by "+strings.Join(blockers, ", "))
	}
	if day := strValue(issue.DeferredUntilDate); day != "" && day > now.Local().Format(dayLayout) {
		notes = append(notes, "deferred until "+day)
	}
	if issue.OpenChildCount > 0 {
		notes = append(notes, fmt.Sprintf("%d open %s", issue.OpenChildCount, plural(issue.OpenChildCount, "child", "children")))
	}
	return notes
}

func claimNote(issue api.Issue, now time.Time) string {
	if issue.Status != api.IssueStatusInProgress {
		return ""
	}
	if issue.ClaimExpiresTS == nil || issue.ClaimedBy == nil {
		return "in progress with no claim"
	}
	if !issue.ClaimExpiresTS.After(now) {
		return "claim by " + *issue.ClaimedBy + " expired"
	}
	return "claimed by " + *issue.ClaimedBy + " until " + localClock(*issue.ClaimExpiresTS, now)
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

func openNumbers(summaries []api.IssueSummary) []string {
	var numbers []string
	for _, summary := range summaries {
		if summary.Status != api.IssueStatusCompleted && summary.Status != api.IssueStatusCanceled {
			numbers = append(numbers, "#"+strconv.Itoa(summary.Number))
		}
	}
	return numbers
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

func summaryMark(status string) string {
	switch status {
	case api.IssueStatusCompleted:
		return markCompleted
	case api.IssueStatusCanceled:
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

	if len(issue.Comments) > 0 {
		_, _ = fmt.Fprintf(out, "\nComments (%d):\n", len(issue.Comments))
		printIssueComments(out, issue.Comments)
	}
}

func printIssueComments(out io.Writer, comments []api.IssueComment) {
	for i, comment := range comments {
		if i > 0 {
			_, _ = fmt.Fprintln(out)
		}
		_, _ = fmt.Fprintf(out, "  #%d  %s  %s\n", i+1, comment.CreatedTS.Local().Format("2006-01-02 15:04"), orDash(strValue(comment.Author)))
		_, _ = fmt.Fprintln(out, indent(comment.Body, "      "))
	}
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

func printIssueVocabulary(out io.Writer, v api.IssueVocabulary) {
	_, _ = fmt.Fprintf(out, "statuses:     %s\n", strings.Join(v.Statuses, ", "))
	_, _ = fmt.Fprintf(out, "types:        %s\n", strings.Join(v.Types, ", "))
	priorities := make([]string, 0, len(v.Priorities))
	for _, p := range v.Priorities {
		priorities = append(priorities, fmt.Sprintf("%d %s", p.Value, p.Name))
	}
	_, _ = fmt.Fprintf(out, "priorities:   %s\n", strings.Join(priorities, ", "))
	_, _ = fmt.Fprintf(out, "initiatives:  %s\n", strings.Join(v.InitiativeStatuses, ", "))
	_, _ = fmt.Fprintln(out, "\nlabels:")
	printIssueLabels(out, v.Labels)
}
