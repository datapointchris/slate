package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/datapointchris/slate/cli/internal/api"
	"github.com/datapointchris/slate/cli/internal/prompt"
	"github.com/datapointchris/slate/cli/internal/repos"
)

// createStatuses are where a new issue may start: accepted, or waiting on triage.
var createStatuses = []string{api.IssueStatusOpen, api.IssueStatusTriage}

// unclosedStatuses are the states complete and cancel act on.
var unclosedStatuses = []string{api.IssueStatusTriage, api.IssueStatusOpen}

// --- Create ---

// issueCreateFields is the record `issues create` builds, in the order it asks.
// The initiatives and labels are read from the API because both change without
// this file changing.
func issueCreateFields(ctx context.Context, client *api.Client) ([]prompt.Field, error) {
	vocabulary, err := client.GetIssueVocabulary(ctx)
	if err != nil {
		return nil, err
	}
	// A someday initiative takes issues as an active one does, and the API's
	// default list holds only the active ones.
	listed, err := client.ListInitiatives(ctx, "all", "", nil)
	if err != nil {
		return nil, err
	}
	registry, err := repos.Load(repos.DefaultPath())
	if err != nil {
		return nil, err
	}
	initiatives := slices.DeleteFunc(listed, func(i api.Initiative) bool {
		return slices.Contains(api.InitiativeStatusesClosed, i.Status)
	})
	initiativeChoices := make([]string, 0, len(initiatives))
	for _, initiative := range initiatives {
		initiativeChoices = append(initiativeChoices, initiativeChoice(initiative))
	}
	slugs := make([]string, 0, len(vocabulary.Labels))
	for _, label := range vocabulary.Labels {
		slugs = append(slugs, label.Slug)
	}
	return []prompt.Field{
		{Key: "title", Label: "Title"},
		{
			Key:      "type",
			Label:    "Type",
			Hint:     "A decision waits on a person, so the agent queue leaves it out.",
			Default:  "task",
			Choices:  api.IssueTypes,
			Validate: prompt.OneOf(api.IssueTypes),
		},
		{
			Key:      "priority",
			Label:    "Priority",
			Hint:     "None takes the parent's or initiative's. With neither, it sorts last.",
			Default:  api.IssuePriorityNames[0],
			Choices:  api.IssuePriorityNames,
			Validate: priorityAnswer,
		},
		{
			Key:      "repo",
			Label:    "Repo",
			Hint:     "The repo this is work on. Leave it empty for work on no repo.",
			Choices:  registry.Names(),
			Optional: true,
			Validate: repoName(registry),
		},
		{
			Key:      "initiative",
			Label:    "Initiative",
			Hint:     "The outcome this ships toward, by number or name. Most issues have none.",
			Choices:  initiativeChoices,
			Optional: true,
			Validate: initiativeRef(initiatives),
		},
		{
			Key:      "label",
			Label:    "Label",
			Choices:  slugs,
			Optional: true,
			Repeat:   true,
			Validate: prompt.OneOf(slugs),
		},
		{
			Key:       "description",
			Label:     "Description",
			Hint:      "What an agent could not find in the repo on its own.",
			Optional:  true,
			Multiline: true,
		},
		{
			Key:       "acceptance",
			Label:     "Acceptance",
			Hint:      "How to verify it is done.",
			Optional:  true,
			Multiline: true,
		},
	}, nil
}

// initiativeChoice is how the form offers an initiative: the number it resolves
// to, then the name, which Tab completion matches anywhere in the choice.
func initiativeChoice(initiative api.Initiative) string {
	return strconv.Itoa(initiative.Number) + " " + initiative.Name
}

// initiativeRef accepts an initiative as N, #N, a UUID, a name or an offered
// choice. A number or a UUID passes straight through for the API to resolve,
// because the choices hold only the initiatives that take issues.
func initiativeRef(initiatives []api.Initiative) func(string) (string, error) {
	choices := make([]string, 0, len(initiatives))
	for _, initiative := range initiatives {
		choices = append(choices, initiativeChoice(initiative))
	}
	byChoice := prompt.OneOf(choices)
	return func(answer string) (string, error) {
		if isItemNumber(strings.TrimPrefix(answer, "#")) || looksLikeUUID(answer) {
			return answer, nil
		}
		for _, initiative := range initiatives {
			if strings.EqualFold(answer, initiative.Name) {
				return initiative.Name, nil
			}
		}
		chosen, err := byChoice(answer)
		if err != nil {
			return "", err
		}
		number, _, _ := strings.Cut(chosen, " ")
		return number, nil
	}
}

// isItemNumber reports whether s is a bare item number, the digits alone.
func isItemNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// priorityAnswer accepts a priority by name or value and answers with its name.
func priorityAnswer(answer string) (string, error) {
	value, err := parsePriority(answer)
	if err != nil {
		return "", err
	}
	return priorityName(value), nil
}

// parseDay refuses anything but a YYYY-MM-DD day, before the API sees it.
func parseDay(flag, value string) error {
	if _, err := time.Parse(dayLayout, value); err != nil {
		return goclikit.UsageError(fmt.Errorf("--%s %q: a day is YYYY-MM-DD", flag, value))
	}
	return nil
}

func newIssuesCreateCommand() *cobra.Command {
	var (
		asJSON         bool
		issueStatus    string
		parent         string
		discoveredFrom string
		dependsOn      []string
		deferredUntil  string
		autofix        bool
	)
	cmd := &cobra.Command{
		Use:   "create [flags]",
		Short: "File an issue",
		Long: "Pass every field as a flag to file in one shot. Leave --title out at a\n" +
			"terminal and the rest are asked for one at a time. Tab lists the choices for\n" +
			"a field that has them. Label repeats until an empty answer, and Description\n" +
			"and Acceptance take lines until a blank one. Ctrl-C abandons the issue.\n" +
			"\n" +
			"--status triage holds it for a person to accept before an agent may take it.\n" +
			"Anything a hook or a script files belongs there.\n" +
			"\n" +
			"--autofix=false keeps it and its children from an unattended fix, so only a\n" +
			"session takes them.\n" +
			"\n" +
			"--parent, --discovered-from, --depends-on, --deferred-until and --autofix are\n" +
			"never asked for. A new issue that depends on its own parent is refused, since\n" +
			"neither could ever start.",
		Example: "  slate issues create\n" +
			"  slate issues create --title \"Routing drops /issues\" --type bug --repo webapp --priority high\n" +
			"  slate issues create --title \"Pick a rank scheme\" --type decision --label needs-design\n" +
			"  slate issues create --title \"Flaky test\" --status triage --discovered-from 412 --description \"$(cat log.md)\"",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f := cmd.Flags()
			if f.Changed("status") && !slices.Contains(createStatuses, issueStatus) {
				return goclikit.UsageError(fmt.Errorf("--status %q: a new issue starts %s", issueStatus, strings.Join(createStatuses, " or ")))
			}
			if f.Changed("deferred-until") {
				if err := parseDay("deferred-until", deferredUntil); err != nil {
					return err
				}
			}
			answers := flagAnswers(cmd, "title", "type", "priority", "repo", "initiative", "label", "description", "acceptance")
			missing := missingFlags(answers, "title")
			if len(missing) > 0 && !interactive(cmd) {
				return goclikit.UsageError(fmt.Errorf("%s required — pass it, or run from a terminal to be asked", strings.Join(missing, " and ")))
			}
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			fields, err := issueCreateFields(cmd.Context(), client)
			if err != nil {
				return handleAPIError(err)
			}
			if err := validateAnswers(answers, fields); err != nil {
				return goclikit.UsageError(err)
			}
			if len(missing) > 0 {
				asked, err := runForm(cmd, prompt.Form{
					Intro:  "Filing an issue. Ctrl-C to abandon it.",
					Fields: unanswered(fields, answers),
				})
				if errors.Is(err, errAborted) {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "\nAborted.")
					return nil
				}
				if err != nil {
					return err
				}
				answers.Merge(asked)
			}
			in := api.IssueCreateInput{Title: answers.Get("title"), Labels: answers.All("label"), DependsOn: dependsOn}
			optional := map[string]**string{
				"type": &in.Type, "repo": &in.Repo, "initiative": &in.Initiative,
				"description": &in.Description, "acceptance": &in.Acceptance,
			}
			for key, target := range optional {
				if answers.Has(key) {
					*target = ptr(answers.Get(key))
				}
			}
			if answers.Has("priority") {
				priority, err := parsePriority(answers.Get("priority"))
				if err != nil {
					return goclikit.UsageError(fmt.Errorf("--priority: %w", err))
				}
				in.Priority = &priority
			}
			in.Status = valuedStrFlag(f, "status", &issueStatus)
			in.Parent = valuedStrFlag(f, "parent", &parent)
			in.DiscoveredFrom = valuedStrFlag(f, "discovered-from", &discoveredFrom)
			in.DeferredUntilDate = valuedStrFlag(f, "deferred-until", &deferredUntil)
			if f.Changed("autofix") {
				in.Autofix = &autofix
			}

			written, err := client.CreateIssue(cmd.Context(), in, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			issue := written.Issue
			return reportWrite(cmd, asJSON, written, written.WriteEffects, fmt.Sprintf("Filed #%d %s (%s)", issue.Number, issue.Title, issue.Status))
		},
	}
	f := cmd.Flags()
	f.String("title", "", "Issue title (asked for when omitted)")
	f.String("type", "", "One of: "+strings.Join(api.IssueTypes, ", ")+" (default task)")
	f.String("priority", "", "One of: "+priorityChoices()+" (default none)")
	f.String("repo", "", "Repo this is work on, by registry name (omit for work on no repo)")
	f.String("initiative", "", "Initiative this ships toward, by number, name or id")
	f.StringArray("label", nil, "Label to carry (repeatable; see: slate issues labels list)")
	f.String("description", "", "What an agent could not find in the repo on its own")
	f.String("acceptance", "", "How to verify it is done")
	f.StringVar(&issueStatus, "status", "", "Where it starts: "+strings.Join(createStatuses, " or ")+" (default open)")
	f.StringVar(&parent, "parent", "", "Parent issue, which waits on this one")
	f.StringVar(&discoveredFrom, "discovered-from", "", "Issue whose work turned this up")
	f.StringArrayVar(&dependsOn, "depends-on", nil, "Issue this waits on (repeatable)")
	f.StringVar(&deferredUntil, "deferred-until", "", "Day before which it is not ready, as YYYY-MM-DD")
	f.BoolVar(&autofix, "autofix", true, autofixSetHelp)
	f.BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

// --- Edit ---

// issueClearableFlags maps each edit flag an empty value clears to the field it
// empties. --label is left out, because labels empty to [] rather than null.
var issueClearableFlags = map[string]string{
	"description": "description", "acceptance": "acceptance", "repo": "repo",
	"initiative": "initiative", "parent": "parent", "discovered-from": "discovered_from",
	"deferred-until": "deferred_until_date",
}

// issueRequiredFlags are the fields every issue carries, so none can be emptied.
var issueRequiredFlags = []string{"title", "type", "priority"}

// autofixSetHelp is the help an issue write's --autofix carries.
const autofixSetHelp = "False keeps it and its children from an unattended fix, as its initiative's or a parent's false does"

type issueEditFlags struct {
	title, description, acceptance, repo, issueType, priority string
	initiative, parent, discoveredFrom, deferredUntil         string
	labels                                                    []string
	autofix                                                   bool
}

func newIssuesEditCommand() *cobra.Command {
	var (
		v      issueEditFlags
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "edit <issue> [flags]",
		Short: "Change an issue's fields",
		Long: "Update only the fields whose flags you pass. Status moves through the\n" +
			"lifecycle verbs instead: accept, complete, cancel and reopen.\n" +
			"\n" +
			"An empty value empties the field: --repo \"\" takes it off every repo and\n" +
			"--deferred-until \"\" makes it ready today. --label replaces the whole set,\n" +
			"and --label \"\" removes every label. --title, --type and --priority cannot\n" +
			"be emptied. --priority none drops the issue's own, so it takes its parent's\n" +
			"or its initiative's. --autofix=false keeps it and its children from an\n" +
			"unattended fix.",
		Example: "  slate issues edit 412 --priority urgent\n" +
			"  slate issues edit 412 --autofix=false\n" +
			"  slate issues edit 412 --acceptance \"$(cat acceptance.md)\"\n" +
			"  slate issues edit 412 --label area-api --label needs-design\n" +
			"  slate issues edit 412 --initiative \"\" --deferred-until 2026-11-01",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in, clear, err := v.update(cmd)
			if err != nil {
				return err
			}
			return runIssueUpdate(cmd, args[0], in, clear, asJSON, "Updated")
		},
	}
	v.register(cmd)
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func (v *issueEditFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&v.title, "title", "", "New title")
	f.StringVar(&v.description, "description", "", "New description (empty clears)")
	f.StringVar(&v.acceptance, "acceptance", "", "New acceptance (empty clears)")
	f.StringVar(&v.repo, "repo", "", "Repo by registry name (empty takes it off every repo)")
	f.StringVar(&v.issueType, "type", "", "One of: "+strings.Join(api.IssueTypes, ", "))
	f.StringVar(&v.priority, "priority", "", "One of: "+priorityChoices())
	f.StringVar(&v.initiative, "initiative", "", "Initiative by number, name or id (empty removes it)")
	f.StringVar(&v.parent, "parent", "", "Parent issue (empty removes it)")
	f.StringVar(&v.discoveredFrom, "discovered-from", "", "Issue whose work turned this up (empty removes it)")
	f.StringVar(&v.deferredUntil, "deferred-until", "", "Day before which it is not ready, as YYYY-MM-DD (empty clears)")
	f.StringArrayVar(&v.labels, "label", nil, "Label (repeatable; replaces the set, empty removes every label)")
	f.BoolVar(&v.autofix, "autofix", true, autofixSetHelp)
}

// update settles every refusal that needs no server state, then builds the body
// and the list of fields to send as null.
func (v *issueEditFlags) update(cmd *cobra.Command) (api.IssueUpdateInput, []string, error) {
	f := cmd.Flags()
	for _, name := range issueRequiredFlags {
		if readFlagIntent(f, name) == flagClears {
			return api.IssueUpdateInput{}, nil, goclikit.UsageError(fmt.Errorf("--%s cannot be emptied — every issue carries one", name))
		}
	}
	if readFlagIntent(f, "label") == flagMixesEmptyWithValues {
		return api.IssueUpdateInput{}, nil, goclikit.UsageError(errors.New("--label given both a value and an empty one — an empty value removes every label, so pass one or the other"))
	}
	if err := validateIssueType(f, "type", v.issueType); err != nil {
		return api.IssueUpdateInput{}, nil, err
	}
	if readFlagIntent(f, "repo") == flagCarriesValues {
		if err := validateRepoFlag(cmd, v.repo); err != nil {
			return api.IssueUpdateInput{}, nil, err
		}
	}
	if readFlagIntent(f, "deferred-until") == flagCarriesValues {
		if err := parseDay("deferred-until", v.deferredUntil); err != nil {
			return api.IssueUpdateInput{}, nil, err
		}
	}

	in := api.IssueUpdateInput{
		Title:             valuedStrFlag(f, "title", &v.title),
		Description:       valuedStrFlag(f, "description", &v.description),
		Acceptance:        valuedStrFlag(f, "acceptance", &v.acceptance),
		Repo:              valuedStrFlag(f, "repo", &v.repo),
		Type:              valuedStrFlag(f, "type", &v.issueType),
		Initiative:        valuedStrFlag(f, "initiative", &v.initiative),
		Parent:            valuedStrFlag(f, "parent", &v.parent),
		DiscoveredFrom:    valuedStrFlag(f, "discovered-from", &v.discoveredFrom),
		DeferredUntilDate: valuedStrFlag(f, "deferred-until", &v.deferredUntil),
	}
	if f.Changed("priority") {
		priority, err := parsePriority(v.priority)
		if err != nil {
			return api.IssueUpdateInput{}, nil, goclikit.UsageError(fmt.Errorf("--priority: %w", err))
		}
		in.Priority = &priority
	}
	if f.Changed("autofix") {
		in.Autofix = &v.autofix
	}
	switch readFlagIntent(f, "label") {
	case flagClears:
		in.Labels = &[]string{}
	case flagCarriesValues:
		in.Labels = &v.labels
	case flagUntouched, flagMixesEmptyWithValues:
	}

	var clear []string
	for name, field := range issueClearableFlags {
		if readFlagIntent(f, name) == flagClears {
			clear = append(clear, field)
		}
	}
	sort.Strings(clear)
	if in == (api.IssueUpdateInput{}) && len(clear) == 0 {
		return api.IssueUpdateInput{}, nil, goclikit.UsageError(errors.New("nothing to change — pass at least one field flag"))
	}
	return in, clear, nil
}

// runIssueUpdate applies a PATCH and reports the issue it left.
func runIssueUpdate(cmd *cobra.Command, ref string, in api.IssueUpdateInput, clear []string, asJSON bool, verb string) error {
	client, err := newAPIClient(cmd.Context())
	if err != nil {
		return handleAPIError(err)
	}
	written, err := client.UpdateIssue(cmd.Context(), ref, in, clear, issueZone())
	if err != nil {
		return handleArgumentAPIError(err)
	}
	return reportIssueWrite(cmd, asJSON, written, verb)
}

func reportIssueWrite(cmd *cobra.Command, asJSON bool, written api.IssueWritten, verb string) error {
	issue := written.Issue
	return reportWrite(cmd, asJSON, written, written.WriteEffects, fmt.Sprintf("%s #%d %s (%s)", verb, issue.Number, issue.Title, issue.Status))
}

func newIssuesDeleteCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <issue>",
		Short: "Delete an issue, its comments, its edges and its claims",
		Long: "For an issue filed by mistake. Work that will not be done is canceled\n" +
			"instead, which keeps the reason. Every claim it has had goes with it, live or\n" +
			"ended. Its children, the issues found during its work and the issues canceled\n" +
			"as its duplicates stay, with the link to it removed. Prompts unless --yes.",
		Example: "  slate issues delete 412 --yes",
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
			if !yes {
				ok, err := confirm(cmd, fmt.Sprintf("Delete #%d %q? This cannot be undone.", issue.Number, issue.Title))
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Aborted.")
					return nil
				}
			}
			if err := client.DeleteIssue(cmd.Context(), issue.ID); err != nil {
				return handleAPIError(err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted #%d %s\n", issue.Number, issue.Title)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}

// --- Lifecycle ---

// runIssueTransition moves an issue between statuses after checking it is in
// one the verb is for. The read and the write are two requests, so this guards
// against the wrong verb rather than against a concurrent edit.
func runIssueTransition(cmd *cobra.Command, ref string, from []string, in api.IssueUpdateInput, asJSON bool, verb string) error {
	client, err := newAPIClient(cmd.Context())
	if err != nil {
		return handleAPIError(err)
	}
	current, err := client.GetIssue(cmd.Context(), ref, issueZone())
	if err != nil {
		return handleArgumentAPIError(err)
	}
	if !slices.Contains(from, current.Status) {
		return fmt.Errorf("#%d is %s — %s applies to an issue that is %s",
			current.Number, current.Status, cmd.Name(), strings.Join(from, " or "))
	}
	written, err := client.UpdateIssue(cmd.Context(), current.ID, in, nil, issueZone())
	if err != nil {
		return handleArgumentAPIError(err)
	}
	return reportIssueWrite(cmd, asJSON, written, verb)
}

func newIssuesAcceptCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "accept <issue>",
		Short:   "Move an issue out of triage and into the queue",
		Long:    "Triage holds what a hook or a script filed until a person says it is real.",
		Example: "  slate issues accept 412",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			open := api.IssueStatusOpen
			return runIssueTransition(cmd, args[0], []string{api.IssueStatusTriage}, api.IssueUpdateInput{Status: &open}, asJSON, "Accepted")
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

// closedByHelp is the flag's usage line on every verb that closes an issue.
const closedByHelp = "Who is closing it (empty records the signed-in user)"

// closer is the actor a close sends. The default names whoever runs slate, so it
// is sent unless emptied, and an empty one leaves the server to record the
// signed-in user.
func closer(cmd *cobra.Command, closedBy *string) *string {
	if readFlagIntent(cmd.Flags(), "closed-by") == flagClears {
		return nil
	}
	return closedBy
}

func newIssuesCompleteCommand() *cobra.Command {
	var (
		closedBy string
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:   "complete <issue>",
		Short: "Mark an issue done",
		Long: "Records when it closed and who closed it, and ends its claim as completed,\n" +
			"printing whose claim it ended. A parent is refused while a child is still\n" +
			"open. Say what shipped with add-comment, so the thread holds it. --closed-by\n" +
			"defaults to who is running slate: the Claude Code session, the worker\n" +
			"$" + actorWorkerEnv + " names, or user@host.",
		Example: "  slate issues complete 412",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			completed := api.IssueStatusCompleted
			in := api.IssueUpdateInput{Status: &completed, ClosedBy: closer(cmd, &closedBy)}
			return runIssueTransition(cmd, args[0], unclosedStatuses, in, asJSON, "Completed")
		},
	}
	cmd.Flags().StringVar(&closedBy, "closed-by", actorIdentity(), closedByHelp)
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newIssuesCancelCommand() *cobra.Command {
	var (
		reason      string
		duplicateOf string
		closedBy    string
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "cancel <issue> --reason <why> | --duplicate-of <issue>",
		Short: "Close an issue that will not be done",
		Long: "Canceling needs a reason, the issue this one duplicates, or both. A bare\n" +
			"cancel invites the same issue back next month. It ends the issue's claim as\n" +
			"canceled, printing whose. --closed-by defaults to who is running slate, as it\n" +
			"does for complete.",
		Example: "  slate issues cancel 412 --reason \"the router rewrite removed the path\"\n" +
			"  slate issues cancel 413 --duplicate-of 412",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in := api.IssueUpdateInput{
				StatusReason: valuedStrFlag(cmd.Flags(), "reason", &reason),
				DuplicateOf:  valuedStrFlag(cmd.Flags(), "duplicate-of", &duplicateOf),
				ClosedBy:     closer(cmd, &closedBy),
			}
			if in.StatusReason == nil && in.DuplicateOf == nil {
				return goclikit.UsageError(errors.New("--reason or --duplicate-of is required — say why it will not be done"))
			}
			canceled := api.IssueStatusCanceled
			in.Status = &canceled
			return runIssueTransition(cmd, args[0], unclosedStatuses, in, asJSON, "Canceled")
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Why it will not be done")
	cmd.Flags().StringVar(&duplicateOf, "duplicate-of", "", "The issue this one duplicates")
	cmd.Flags().StringVar(&closedBy, "closed-by", actorIdentity(), closedByHelp)
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newIssuesReopenCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "reopen <issue>",
		Short: "Return a completed or canceled issue to the queue",
		Long: "Clears when and by whom it closed, the cancel reason, and the duplicate link.\n" +
			"A completed issue holds no open work, so each completed ancestor reopens\n" +
			"with it, and each one reopened prints under the issue.",
		Example: "  slate issues reopen 412",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			open := api.IssueStatusOpen
			closed := []string{api.IssueStatusCompleted, api.IssueStatusCanceled}
			return runIssueTransition(cmd, args[0], closed, api.IssueUpdateInput{Status: &open}, asJSON, "Reopened")
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

// --- Order and dependencies ---

func newIssuesReorderCommand() *cobra.Command {
	var (
		before string
		after  string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "reorder <issue> --before <issue> | --after <issue>",
		Short: "Move an issue directly before or after another in the queue",
		Long: "The queue sorts by priority first. Rank only orders issues of the same\n" +
			"effective priority, so the issue named by --before or --after must share it.\n" +
			"One of another priority is refused. `edit --priority` moves an issue across\n" +
			"priorities.",
		Example: "  slate issues reorder 412 --before 398\n  slate issues reorder 412 --after 420",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (before == "") == (after == "") {
				return goclikit.UsageError(errors.New("pass exactly one of --before and --after"))
			}
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.RankIssue(cmd.Context(), args[0], api.IssueRankMove{Before: before, After: after}, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			where := "before " + issueRefLabel(before)
			if after != "" {
				where = "after " + issueRefLabel(after)
			}
			issue := written.Issue
			return reportWrite(cmd, asJSON, written, written.WriteEffects, fmt.Sprintf("Moved #%d %s %s", issue.Number, issue.Title, where))
		},
	}
	cmd.Flags().StringVar(&before, "before", "", "Issue to place it directly ahead of")
	cmd.Flags().StringVar(&after, "after", "", "Issue to place it directly behind")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

// issueRefLabel prints a reference the way issue rows print a number.
func issueRefLabel(ref string) string {
	if _, err := strconv.Atoi(ref); err == nil {
		return "#" + ref
	}
	return ref
}

func newIssuesAddDependencyCommand() *cobra.Command {
	var (
		dependsOn string
		asJSON    bool
	)
	cmd := &cobra.Command{
		Use:   "add-dependency <issue> --depends-on <other-issue>",
		Short: "Record that an issue waits on another",
		Long: "An issue is not ready while anything it depends on is unclosed. The issue it\n" +
			"waits on takes its priority when that is more urgent. An edge that would\n" +
			"leave an issue waiting on itself, through dependencies or a parent, is\n" +
			"refused, and the refusal prints the path.",
		Example: "  slate issues add-dependency 412 --depends-on 411",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dependsOn == "" {
				return goclikit.UsageError(errors.New("--depends-on is required"))
			}
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.AddIssueDependency(cmd.Context(), args[0], dependsOn, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			issue := written.Issue
			summary := fmt.Sprintf("#%d %s now waits on %s", issue.Number, issue.Title, strings.Join(dependencyNumbers(issue.DependsOn), ", "))
			return reportWrite(cmd, asJSON, written, written.WriteEffects, summary)
		},
	}
	cmd.Flags().StringVar(&dependsOn, "depends-on", "", "Issue it waits on (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func dependencyNumbers(summaries []api.IssueSummary) []string {
	numbers := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		numbers = append(numbers, "#"+strconv.Itoa(summary.Number))
	}
	return numbers
}

func newIssuesRemoveDependencyCommand() *cobra.Command {
	var (
		dependsOn string
		yes       bool
	)
	cmd := &cobra.Command{
		Use:     "remove-dependency <issue> --depends-on <other-issue>",
		Short:   "Remove the edge between an issue and one it waits on",
		Example: "  slate issues remove-dependency 412 --depends-on 411",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dependsOn == "" {
				return goclikit.UsageError(errors.New("--depends-on is required"))
			}
			if !yes {
				ok, err := confirm(cmd, fmt.Sprintf("Remove the dependency %s → %s?", args[0], dependsOn))
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Aborted.")
					return nil
				}
			}
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			if err := client.RemoveIssueDependency(cmd.Context(), args[0], dependsOn); err != nil {
				return handleArgumentAPIError(err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed the dependency %s → %s\n", args[0], dependsOn)
			return nil
		},
	}
	cmd.Flags().StringVar(&dependsOn, "depends-on", "", "Issue it no longer waits on (required)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}

// --- Comments ---

func newIssuesAddCommentCommand() *cobra.Command {
	var (
		body   string
		author string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "add-comment <issue> [<text>]",
		Short: "Leave a comment on an issue",
		Long: "Progress, a handoff, or what completing it shipped. The comment is markdown,\n" +
			"given after the issue or in --body. One that opens with - goes after --.\n" +
			"--author defaults to who is running slate: the Claude Code session, the worker\n" +
			"$" + actorWorkerEnv + " names, or user@host.",
		Example: "  slate issues add-comment 412 \"Root cause: the router order\"\n" +
			"  slate issues add-comment 412 -- \"- shipped the rules\"\n" +
			"  slate issues add-comment 412 --body \"$(cat handoff.md)\"",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 2 {
				if cmd.Flags().Changed("body") {
					return goclikit.UsageError(errors.New("give the comment once — after the issue, or in --body"))
				}
				body = args[1]
			}
			if strings.TrimSpace(body) == "" {
				return goclikit.UsageError(errors.New("the comment is required and cannot be blank — give it after the issue, or in --body"))
			}
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			// The default names the session, so it is sent unless emptied.
			signedBy := &author
			if readFlagIntent(cmd.Flags(), "author") == flagClears {
				signedBy = nil
			}
			written, err := client.AddIssueComment(cmd.Context(), args[0], body, signedBy)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			summary := fmt.Sprintf("Commented on %s as %s", args[0], orDash(strValue(written.Comment.Author)))
			return reportWrite(cmd, asJSON, written, written.WriteEffects, summary)
		},
	}
	cmd.Flags().StringVar(&body, "body", "", "The comment, as markdown, in place of the text after the issue")
	cmd.Flags().StringVar(&author, "author", actorIdentity(), "Who wrote it (empty leaves it unsigned)")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	// A comment that opens with a bullet parses as a group of short flags.
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		var unknown *pflag.NotExistError
		if errors.As(err, &unknown) && unknown.GetSpecifiedShortnames() != "" {
			err = fmt.Errorf("%w — a comment that opens with - goes after --, as in: slate issues add-comment 412 -- \"- shipped\"", err)
		}
		return goclikit.UsageError(err)
	})
	return cmd
}

func newIssuesRemoveCommentCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "remove-comment <issue> <comment>",
		Short:   "Delete one comment from an issue",
		Long:    "The comment is its #n in `slate issues comments`, or its id.",
		Example: "  slate issues remove-comment 412 3",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			comment, err := resolveIssueComment(cmd.Context(), client, args[0], args[1])
			if err != nil {
				return err
			}
			if !yes {
				ok, err := confirm(cmd, fmt.Sprintf("Delete this comment on %s?\n%s", args[0], indent(comment.Body, "  ")))
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Aborted.")
					return nil
				}
			}
			if err := client.RemoveIssueComment(cmd.Context(), args[0], comment.ID); err != nil {
				return handleArgumentAPIError(err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted the comment on %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	return cmd
}

// resolveIssueComment finds a comment by its place in the thread or by id. The
// place is what the comment rows print, so a row can be acted on as read.
func resolveIssueComment(ctx context.Context, client *api.Client, issue, ref string) (api.IssueComment, error) {
	comments, err := client.ListIssueComments(ctx, issue, nil)
	if err != nil {
		return api.IssueComment{}, handleArgumentAPIError(err)
	}
	if looksLikeUUID(ref) {
		for _, comment := range comments {
			if comment.ID == ref {
				return comment, nil
			}
		}
		return api.IssueComment{}, fmt.Errorf("no comment %s on %s — slate issues comments %s", ref, issue, issue)
	}
	place, err := strconv.Atoi(strings.TrimPrefix(ref, "#"))
	if err != nil {
		return api.IssueComment{}, goclikit.UsageError(fmt.Errorf("comment %q: pass its #n from `slate issues comments %s`, or its id", ref, issue))
	}
	if place < 1 || place > len(comments) {
		return api.IssueComment{}, fmt.Errorf("%s has %d %s, so there is no #%d", issue, len(comments), plural(len(comments), "comment", "comments"), place)
	}
	return comments[place-1], nil
}
