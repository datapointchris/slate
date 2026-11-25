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

var ideaHints = []string{anyRecordHint, "List every idea: slate ideas list"}

// promoteStatuses are where a promoted idea's initiative may start.
var promoteStatuses = []string{"active", "someday"}

func newIdeasCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "ideas",
		Short: "Keep the work nobody has taken on, with its priority and refs",
		Long: "An idea is work nobody has taken on yet. It carries its own priority, like any\n" +
			"record, and links what it rests on by key or URL. slate stores each ref as\n" +
			"given and never follows it, so a key from any system works.\n" +
			"\n" +
			"An idea is never work, so it has no status. `promote` turns it into the\n" +
			"initiative or issue that is the work, under the same number. `fold` ends it\n" +
			"into work that already covers it, and its number then leads there. An idea\n" +
			"shares its number sequence with initiatives and issues.",
		Example: "  slate ideas create --title \"Scheduled review rounds\" --priority high  keep it for later\n" +
			"  slate ideas link 1860 https://example.com/thread/9                    link what it rests on\n" +
			"  slate ideas list                                                      every idea, most urgent first\n" +
			"  slate ideas promote 1860 --summary \"Rounds run on a schedule\"         take it on as an initiative\n" +
			"  slate ideas fold 1860 --into 1501                                     end it into work that covers it",
	})
	goclikit.WithRecoveryHints(cmd, ideaHints...)
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupReading,
		newIdeasListCommand(),
		goclikit.WithRecoveryHints(newIdeasShowCommand(), ideaHints...),
	)...)
	cmd.AddCommand(inGroup(groupChanging,
		newIdeasCreateCommand(),
		goclikit.WithRecoveryHints(newIdeasEditCommand(), ideaHints...),
		goclikit.WithRecoveryHints(newRefsLinkCommand("idea",
			"  slate ideas link 1860 capture/01a1b689-a5d6-7852-b675-6acbd13ece24\n"+
				"  slate ideas link 1860 https://example.com/thread/9 --note \"Where it came up\""), ideaHints...),
		goclikit.WithRecoveryHints(newIdeasPromoteCommand(), ideaHints...),
		goclikit.WithRecoveryHints(newIdeasFoldCommand(), ideaHints...),
		goclikit.WithRecoveryHints(newIdeasReorderCommand(), ideaHints...),
		goclikit.WithRecoveryHints(newIdeasPinCommand(), ideaHints...),
		goclikit.WithRecoveryHints(newIdeasUnpinCommand(), ideaHints...),
		goclikit.WithRecoveryHints(newIdeasDeleteCommand(), ideaHints...),
	)...)
	return cmd
}

func newIdeasDeleteCommand() *cobra.Command {
	var (
		yes    bool
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "delete <idea> [--yes]",
		Short: "Delete an idea with its refs, pin and claims",
		Long: "Every claim it has had goes with it, live or ended, with a warning when\n" +
			"another holder's live claim was one. It prints each claim and worklog it\n" +
			"took. An idea already promoted is refused, naming its initiative, because\n" +
			"that initiative names it as where the work came from. Prompts unless --yes.",
		Example:     "  slate ideas delete 1860 --yes",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			if !yes {
				idea, err := client.GetIdea(cmd.Context(), args[0])
				if err != nil {
					return handleArgumentAPIError(err)
				}
				if !answeredAnother(args[0], idea.Number) {
					question := fmt.Sprintf("Delete idea #%d %q and its %d %s?",
						idea.Number, idea.Title, len(idea.Refs), plural(len(idea.Refs), "ref", "refs"))
					ok, err := confirm(cmd, question)
					if err != nil {
						return err
					}
					if !ok {
						_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Aborted.")
						return nil
					}
				}
			}
			deleted, err := client.DeleteIdea(cmd.Context(), args[0], issueZone())
			if err != nil {
				return handleAPIError(err)
			}
			summary := fmt.Sprintf("Deleted idea #%d %q", deleted.Deleted.Number, deleted.Deleted.Title)
			return reportWrite(cmd, asJSON, deleted, deleted.WriteEffects, summary)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newIdeasReorderCommand() *cobra.Command {
	return newReorderCommand("idea",
		"`slate next` lists an idea at step discuss until it is promoted.",
		"  slate ideas reorder 1530 --before 1491")
}

func newIdeasListCommand() *cobra.Command {
	var (
		asJSON  bool
		tag     string
		pursuit string
		ref     string
		limit   int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every idea, promoted or not, most urgent first",
		Long: "Priority none sorts after every level. Every number that is or was an idea\n" +
			"is listed once. BECAME names what an idea turned into: the initiative or issue\n" +
			"it was promoted to, or the record it folded into, and — while it is open.\n" +
			"--json carries it as became, with how it ended and that record as it stands.\n" +
			"--tag, --pursuit and --ref leave out folded ideas, whose rows moved.",
		Example:     "  slate ideas list\n  slate ideas list --tag security\n  slate ideas list --ref capture/\n  slate ideas list --json",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			ideas, err := client.ListIdeas(cmd.Context(), api.IdeaFilter{Tag: tag, Pursuit: pursuit, Ref: ref}, limitFlag(cmd))
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if err := honoredGroupings(groupingFilter{tag, pursuit, ref}, ideas, ideaGrouped); err != nil {
				return err
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), ideas)
			}
			printIdeasTable(cmd.OutOrStdout(), ideas)
			return nil
		},
	}
	addLimitFlag(cmd, &limit)
	cmd.Flags().StringVar(&tag, "tag", "", tagFilterHelp)
	cmd.Flags().StringVar(&pursuit, "pursuit", "", pursuitFilterHelp)
	cmd.Flags().StringVar(&ref, "ref", "", refFilterHelp)
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output ideas as JSON to stdout")
	return cmd
}

func newIdeasShowCommand() *cobra.Command {
	var (
		asJSON      bool
		changeCount int
	)
	cmd := &cobra.Command{
		Use:         "show <idea>",
		Short:       "Show an idea with its refs, its claims, the initiative it became, and its changes",
		Example:     "  slate ideas show 1860\n  slate ideas show 1860 --json",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			idea, err := client.GetIdea(cmd.Context(), args[0])
			if err != nil {
				return handleArgumentAPIError(err)
			}
			changes, err := readChanges(cmd, client, idea.Number, changeCount, asJSON)
			if err != nil {
				return err
			}
			defer noteChanges(cmd, changes)
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), ideaShown{IdeaDetail: idea, Changes: changes})
			}
			out, now := cmd.OutOrStdout(), time.Now()
			printIdeaDetail(out, idea, now)
			printChanges(out, changes, now)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the idea as JSON to stdout")
	addChangesFlag(cmd, &changeCount)
	return cmd
}

// ideaShown is what every show of an idea prints as JSON: the idea, with the
// changes the show read beside its own fields.
type ideaShown struct {
	api.IdeaDetail
	Changes *recordChanges `json:"changes,omitempty"`
}

func newIdeasCreateCommand() *cobra.Command {
	var (
		title       string
		description string
		priority    string
		tags        []string
		pursuits    []string
		refs        []string
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "create --title <title> [flags]",
		Short: "Record an idea",
		Example: "  slate ideas create --title \"Run review rounds on a schedule\" --priority high --tag agents\n" +
			"  slate ideas create --title \"Run review rounds on a schedule\" --ref capture/01a1b689-a5d6-7852-b675-6acbd13ece24",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsRead, scopeIdeasWrite),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !slices.ContainsFunc(pursuits, namesPursuitByName) {
				narrowGrant(cmd, scopeIdeasWrite)
			}
			if strings.TrimSpace(title) == "" {
				return goclikit.UsageError(errors.New("--title is required"))
			}
			if err := refuseBlankRef(refs); err != nil {
				return err
			}
			f := cmd.Flags()
			in := api.IdeaCreateInput{Title: title, Description: valuedStrFlag(f, "description", &description), Tags: tags, Refs: refs}
			if f.Changed("priority") {
				value, err := parsePriority(priority)
				if err != nil {
					return goclikit.UsageError(fmt.Errorf("--priority: %w", err))
				}
				in.Priority = &value
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			if in.Pursuits, err = pursuitNumbers(cmd, client, pursuits); err != nil {
				return err
			}
			written, err := client.CreateIdea(cmd.Context(), in)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if err := reportWrite(cmd, asJSON, written, written.WriteEffects, ideaLine("Recorded", written.Idea)); err != nil {
				return err
			}
			if err := honoredTagWrite(&in.Tags, written.Idea.Number, written.Idea.Tags); err != nil {
				return err
			}
			if err := honoredPursuitWrite(&in.Pursuits, written.Idea.Number, written.Idea.Pursuits); err != nil {
				return err
			}
			return honoredRefWrite(refs, written.Idea.Number, written.Idea.Refs)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "What the idea is, in a line (required)")
	cmd.Flags().StringVar(&description, "description", "", "The idea in full")
	cmd.Flags().StringVar(&priority, "priority", "", "One of: "+priorityChoices()+" (default none)")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, tagSetHelp)
	cmd.Flags().StringArrayVar(&pursuits, "pursuit", nil, pursuitSetHelp)
	cmd.Flags().StringArrayVar(&refs, "ref", nil, refSetHelp)
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newIdeasEditCommand() *cobra.Command {
	var (
		title       string
		description string
		priority    string
		tags        []string
		pursuits    []string
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "edit <idea> [flags]",
		Short: "Change an idea's title, description, priority, tags or pursuits",
		Long: "Update only the fields whose flags you pass. --description \"\" clears it.\n" +
			"--tag and --pursuit each replace the whole set, and --tag \"\" or\n" +
			"--pursuit \"\" removes every one.",
		Example:     "  slate ideas edit 1860 --priority urgent\n  slate ideas edit 1860 --tag agents --tag security",
		SuggestFor:  editMistakenFor,
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			if readFlagIntent(f, "title") == flagClears {
				return goclikit.UsageError(errors.New("--title cannot be emptied — every idea carries one"))
			}
			in := api.IdeaUpdateInput{Title: valuedStrFlag(f, "title", &title), Description: valuedStrFlag(f, "description", &description)}
			if f.Changed("priority") {
				value, err := parsePriority(priority)
				if err != nil {
					return goclikit.UsageError(fmt.Errorf("--priority: %w", err))
				}
				in.Priority = &value
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
			if in == (api.IdeaUpdateInput{}) && len(clear) == 0 && !f.Changed("pursuit") {
				return goclikit.UsageError(errors.New("nothing to change — pass at least one of --title/--description/--priority/--tag/--pursuit"))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			if in.Pursuits, err = pursuitsEdited(cmd, client, f, pursuits); err != nil {
				return err
			}
			written, err := client.UpdateIdea(cmd.Context(), args[0], in, clear)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if err := reportWrite(cmd, asJSON, written, written.WriteEffects, ideaLine("Updated", written.Idea)); err != nil {
				return err
			}
			if err := honoredTagWrite(in.Tags, written.Idea.Number, written.Idea.Tags); err != nil {
				return err
			}
			return honoredPursuitWrite(in.Pursuits, written.Idea.Number, written.Idea.Pursuits)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "New title")
	cmd.Flags().StringVar(&description, "description", "", "New description (empty clears)")
	cmd.Flags().StringVar(&priority, "priority", "", "One of: "+priorityChoices())
	cmd.Flags().StringArrayVar(&tags, "tag", nil, tagReplaceHelp)
	cmd.Flags().StringArrayVar(&pursuits, "pursuit", nil, pursuitReplaceHelp)
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

// promoteFieldOfFlag is the field each flag of `ideas promote` sets, by the
// JSON name that api.PromoteFieldsOfKind and api.PromoteSharedFields use.
var promoteFieldOfFlag = map[string]string{
	"description": "description",
	"priority":    "priority",
	"tag":         "tags",
	"summary":     "summary",
	"name":        "name",
	"status":      "status",
	"autofix":     "autofix",
	"pursuit":     "pursuits",
	"not-planned": "not_planned_steps",
	"repo":        "repo",
	"path":        "paths",
	"type":        "type",
	"acceptance":  "acceptance",
	"initiative":  "initiative",
	"parent":      "parent",
}

// promoteKindNames is how a refusal names each kind.
var promoteKindNames = map[string]string{api.PromotedToInitiative: "an initiative", api.PromotedToIssue: "--to issue"}

// refuseFlagsOfOtherKind refuses the first flag set whose field applies to a
// kind that --to did not name, before any request.
func refuseFlagsOfOtherKind(f *pflag.FlagSet, kind string) error {
	var refused error
	f.Visit(func(flag *pflag.Flag) {
		for _, other := range api.PromoteKinds {
			if refused == nil && other != kind && slices.Contains(api.PromoteFieldsOfKind[other], promoteFieldOfFlag[flag.Name]) {
				refused = goclikit.UsageError(fmt.Errorf("--%s applies to %s, not %s", flag.Name, promoteKindNames[other], promoteKindNames[kind]))
			}
		}
	})
	return refused
}

// promotedLine is the line naming a promotion, which keeps the idea's number.
func promotedLine(number int, kind, repo string) string {
	if repo != "" {
		kind += " in " + repo
	}
	return fmt.Sprintf("Promoted #%d to %s, under the same number.", number, kind)
}

func newIdeasPromoteCommand() *cobra.Command {
	var (
		to          string
		description string
		priority    string
		tags        []string
		summary     string
		name        string
		status      string
		autofix     bool
		pursuits    []string
		notPlanned  []string
		repo        string
		paths       []string
		issueType   string
		acceptance  string
		initiative  string
		parent      string
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "promote <idea> [--to issue] [flags]",
		Short: "Turn an idea into its initiative or issue, under the same number",
		Long: "Turns the idea into an initiative, or with --to issue an issue, under the same\n" +
			"number. Its comments, documents, refs, tags and checks stay on the number, and\n" +
			"so do its place, pin and flow in the queue. The idea's claim and each open\n" +
			"worklog end as promoted, printed under the line, because promotion closes the\n" +
			"shape step. A flag of the other kind is refused before any request.\n" +
			"\n" +
			"--description, --priority and --tag apply to both kinds, and each left out\n" +
			"keeps the idea's own. --tag \"\" promotes it with none.\n" +
			"\n" +
			"An initiative needs --summary, because an idea has none. --name defaults to\n" +
			"the idea's title, and --pursuit to its pursuits; --pursuit \"\" leaves none.\n" +
			"--status someday promotes it set aside and clears its pin. --not-planned names\n" +
			"each step it will not run, so `slate next` never offers it; an idea marks none,\n" +
			"so left out the initiative plans every step.\n" +
			"\n" +
			"An issue takes --repo, --path, --type, --acceptance, --initiative and --parent,\n" +
			"and starts open. It reads its pursuits from its initiative, so the idea's are\n" +
			"dropped with a warning naming each.\n" +
			"\n" +
			"An idea that already names an initiative of its own is refused, naming it.",
		Example: "  slate ideas promote 1860 --summary \"Review rounds run on a schedule\"\n" +
			"  slate ideas promote 1860 --name \"Scheduled review rounds\" --summary \"Review rounds run on a schedule\" --status someday\n" +
			"  slate ideas promote 1860 --summary \"Review rounds run on a schedule\" --not-planned shape\n" +
			"  slate ideas promote 1860 --to issue --repo webapp --path src/rounds --type feature --acceptance \"A round starts on schedule\"",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			if !slices.Contains(api.PromoteKinds, to) {
				return goclikit.UsageError(fmt.Errorf("unknown --to %q — one of: %s", to, strings.Join(api.PromoteKinds, ", ")))
			}
			if err := refuseFlagsOfOtherKind(f, to); err != nil {
				return err
			}
			in := api.IdeaPromoteInput{Description: valuedStrFlag(f, "description", &description)}
			if f.Changed("priority") {
				value, err := parsePriority(priority)
				if err != nil {
					return goclikit.UsageError(fmt.Errorf("--priority: %w", err))
				}
				in.Priority = &value
			}
			tagSet, err := tagsEdited(f, tags)
			if err != nil {
				return err
			}
			in.Tags = tagSet
			if to == api.PromotedToIssue {
				if f.Changed("type") && !slices.Contains(api.IssueTypes, issueType) {
					return goclikit.UsageError(fmt.Errorf("unknown --type %q — one of: %s", issueType, strings.Join(api.IssueTypes, ", ")))
				}
				in.Repo = valuedStrFlag(f, "repo", &repo)
				in.Paths = paths
				in.Type = valuedStrFlag(f, "type", &issueType)
				in.Acceptance = valuedStrFlag(f, "acceptance", &acceptance)
				in.Initiative = valuedStrFlag(f, "initiative", &initiative)
				in.Parent = valuedStrFlag(f, "parent", &parent)
				return promoteToIssue(cmd, args[0], in, asJSON)
			}

			if f.Changed("status") && !slices.Contains(promoteStatuses, status) {
				return goclikit.UsageError(fmt.Errorf("unknown status %q — one of: %s", status, strings.Join(promoteStatuses, ", ")))
			}
			if readFlagIntent(f, "name") == flagClears {
				return goclikit.UsageError(errors.New("--name cannot be empty — leave it out to take the idea's title"))
			}
			if strings.TrimSpace(summary) == "" {
				return goclikit.UsageError(errors.New("--summary is required: " + initiativeSummaryHelp))
			}
			in.Summary = summary
			in.Name = valuedStrFlag(f, "name", &name)
			in.Status = status
			if f.Changed("autofix") {
				in.Autofix = &autofix
			}
			steps, err := notPlannedEdited(f, notPlanned)
			if err != nil {
				return err
			}
			if steps != nil {
				in.NotPlannedSteps = *steps
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			if in.Pursuits, err = pursuitsEdited(cmd, client, f, pursuits); err != nil {
				return err
			}
			written, err := client.PromoteIdea(cmd.Context(), args[0], in, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			promoted := written.Initiative
			if promoted.Number == 0 {
				return fmt.Errorf("the API's answer held no initiative; `slate show %s` reads what the promotion made", args[0])
			}
			if err := reportWrite(cmd, asJSON, written, written.WriteEffects, promotedLine(promoted.Number, "an initiative", "")); err != nil {
				return err
			}
			if err := honoredTagWrite(in.Tags, promoted.Number, promoted.Tags); err != nil {
				return err
			}
			if err := honoredNotPlannedWrite(steps, promoted.Number, promoted.NotPlannedSteps); err != nil {
				return err
			}
			return honoredPursuitWrite(in.Pursuits, promoted.Number, promoted.Pursuits)
		},
	}
	f := cmd.Flags()
	f.StringVar(&to, "to", api.PromotedToInitiative, "What the idea becomes: "+strings.Join(api.PromoteKinds, " or "))
	withFlagValues(cmd, "to", api.PromoteKinds...)
	f.StringVar(&description, "description", "", "The description it carries (default the idea's)")
	f.StringVar(&priority, "priority", "", "One of: "+priorityChoices()+" (default the idea's)")
	f.StringArrayVar(&tags, "tag", nil, "Tag it carries (repeatable; default the idea's, empty for none)")
	f.StringVar(&summary, "summary", "", initiativeSummaryHelp+" (required for an initiative)")
	f.StringVar(&name, "name", "", "The initiative's name (default the idea's title)")
	f.StringVar(&status, "status", "", "One of: "+strings.Join(promoteStatuses, ", ")+" (default active)")
	f.BoolVar(&autofix, "autofix", false, initiativeAutofixHelp)
	f.StringArrayVar(&pursuits, "pursuit", nil, "Pursuit the initiative is in, by number or exact name (repeatable; default the idea's, empty for none)")
	f.StringArrayVar(&notPlanned, "not-planned", nil, notPlannedSetHelp)
	withFlagValues(cmd, "not-planned", api.InitiativeOptionalSteps...)
	f.StringVar(&repo, "repo", "", "Repo the issue is work on, by registry name (omit for work on no repo)")
	f.StringArrayVar(&paths, "path", nil, issuePathHelp)
	f.StringVar(&issueType, "type", "", "One of: "+strings.Join(api.IssueTypes, ", ")+" (default task)")
	withFlagValues(cmd, "type", api.IssueTypes...)
	f.StringVar(&acceptance, "acceptance", "", "How to verify the issue is done")
	f.StringVar(&initiative, "initiative", "", "Initiative the issue ships toward, by number, name or id")
	f.StringVar(&parent, "parent", "", "Parent issue, which waits on this one")
	f.BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

// promoteToIssue sends a promotion to an issue and prints what it did.
func promoteToIssue(cmd *cobra.Command, ref string, in api.IdeaPromoteInput, asJSON bool) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return handleAPIError(err)
	}
	written, err := client.PromoteIdeaToIssue(cmd.Context(), ref, in, issueZone())
	if err != nil {
		return handleArgumentAPIError(err)
	}
	issue := written.Issue
	if err := reportWrite(cmd, asJSON, written, written.WriteEffects, promotedLine(issue.Number, "an issue", strValue(issue.Repo))); err != nil {
		return err
	}
	return honoredTagWrite(in.Tags, issue.Number, issue.Tags)
}

func newIdeasFoldCommand() *cobra.Command {
	var (
		into   string
		holder string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "fold <idea> --into <record>",
		Short: "Fold an idea into the idea, initiative or issue that covers its work",
		Long: "Moves every row of the idea to the record --into names: its worklogs,\n" +
			"renumbered after that record's last, its claims, comments, checks and\n" +
			"documents, and its refs, tags and pursuits where the record lacks them. A\n" +
			"document whose name the record holds moves as <name>-from-<idea>.\n" +
			"\n" +
			"The idea's number then leads to the record. `slate show` follows it, and a\n" +
			"write through it is refused, naming the record to write to. Each worklog's\n" +
			"old name leads to that worklog under its new name, and a write through it\n" +
			"reaches the worklog. Each document's old address reads that document,\n" +
			"wherever later folds move it.\n" +
			"\n" +
			"The record keeps its own number, fields and place in the queue, so the idea's\n" +
			"pin is dropped with a warning. A closed record is taken, because the idea's\n" +
			"work may already have shipped there. A fold has no undo.\n" +
			"\n" +
			"Another session's live claim on either record refuses the fold, and so does an\n" +
			"initiative that names the idea as where its work came from. The --holder's\n" +
			"claim on the idea moves to the record, or ends as released where that holder\n" +
			"holds the record too. --holder defaults to you.",
		Example:     "  slate ideas fold 4086 --into 2356",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(into) == "" {
				return goclikit.UsageError(errors.New("--into is required: the idea, initiative or issue that covers the idea's work"))
			}
			holder, err := narrowedActor(cmd, "holder", holder)
			if err != nil {
				return err
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			idea, err := client.GetIdea(cmd.Context(), args[0])
			if err != nil {
				return handleArgumentAPIError(err)
			}
			written, err := client.FoldIdea(cmd.Context(), args[0], api.IdeaFoldInput{Into: into, Holder: holder}, issueZone())
			if err != nil {
				if noRouteFor(err) {
					return errors.New("this API has no fold: it answered that no route serves POST /ideas/{id}/fold. " +
						"The fold arrives with a later API release, so nothing was folded")
				}
				return handleArgumentAPIError(err)
			}
			summary, err := foldedLine(idea.Number, idea.Title, written)
			if err != nil {
				return fmt.Errorf("%w; `slate show %s` reads what the fold made", err, into)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, summary)
		},
	}
	cmd.Flags().StringVar(&into, "into", "", "The idea, initiative or issue that covers the idea's work, by number")
	cmd.Flags().StringVar(&holder, "holder", actorIdentity(), "Whose claim on the idea moves with it")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

// noRouteFor reports whether the API answered that it serves no such route,
// as an API from before a route was added does. Every released API words that
// 404 as `no route answers <path>` and sends no field to branch on, so the
// prefix is the one signal an older API gives.
func noRouteFor(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.NotFound() && strings.HasPrefix(apiErr.Message, "no route answers ")
}

// foldedLine names the idea a fold ended and the record it went into, and
// that record's status where it is closed.
func foldedLine(number int, title string, written api.FoldWritten) (string, error) {
	var kind, name, closed string
	var target int
	switch {
	case written.Initiative != nil:
		kind, target, name = "initiative", written.Initiative.Number, written.Initiative.Name
		if slices.Contains(api.InitiativeStatusesClosed, written.Initiative.Status) {
			closed = written.Initiative.Status
		}
	case written.Issue != nil:
		kind, target, name = "issue", written.Issue.Number, written.Issue.Title
		if isClosedIssue(written.Issue.Issue) {
			closed = written.Issue.Status
		}
	case written.Idea != nil:
		kind, target, name = "idea", written.Idea.Number, written.Idea.Title
	default:
		return "", errors.New("the API's answer named no idea, initiative or issue")
	}
	line := fmt.Sprintf("Folded idea #%d %q into %s #%d %q", number, title, kind, target, name)
	if closed != "" {
		line += ", which is " + closed
	}
	return line + ".", nil
}

// answeredAnother reports whether a read of ref answered another record, as a
// read through a number a fold retired does.
func answeredAnother(ref string, number int) bool {
	asked, err := strconv.Atoi(strings.TrimPrefix(ref, "#"))
	return err == nil && asked != number
}

func ideaLine(verb string, idea api.Idea) string {
	return fmt.Sprintf("%s idea #%d %q", verb, idea.Number, idea.Title)
}

// initiativeProgress is an initiative's status and how much of its work is done.
func initiativeProgress(initiative api.Initiative) string {
	return fmt.Sprintf("%s, %d of %d %s done, %d open",
		initiative.Status, initiative.CompletedCount, initiative.IssueCount-initiative.CanceledCount,
		plural(initiative.IssueCount-initiative.CanceledCount, "issue", "issues"), initiative.OpenCount)
}

func printIdeasTable(out io.Writer, ideas []api.Idea) {
	if len(ideas) == 0 {
		_, _ = fmt.Fprintln(out, "No ideas.")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "IDEA\tPRIORITY\tREFS\tBECAME\tTITLE")
	for _, idea := range ideas {
		_, _ = fmt.Fprintf(tw, "#%d\t%s\t%d\t%s\t%s\n", idea.Number, priorityName(idea.Priority), len(idea.Refs), becameCell(idea.Became), idea.Title)
	}
	_ = tw.Flush()
}

// becameCell names what an idea became: the kind and number it was promoted
// to, the record it folded into, or a dash while it is open.
func becameCell(became *api.Became) string {
	switch {
	case became == nil:
		return "—"
	case became.How == api.IdeaPromoted:
		return fmt.Sprintf("%s #%d", became.Kind, became.Number)
	case became.How == api.IdeaFolded:
		return fmt.Sprintf("folded into #%d", became.Number)
	default:
		return fmt.Sprintf("%s #%d", became.How, became.Number)
	}
}

func printIdeaDetail(out io.Writer, idea api.IdeaDetail, now time.Time) {
	p := gotermstyle.PaletteFor(out)
	_, _ = fmt.Fprintf(out, "%s  %s\n", p.Cyan("#"+strconv.Itoa(idea.Number)), p.Bold(idea.Title))
	priority := tierColor(p, idea.Priority)(priorityName(idea.Priority))
	_, _ = fmt.Fprintf(out, "  priority:    %s\n", withPin(priority, idea.PinnedTS, idea.PinnedBy))
	if len(idea.Tags) > 0 {
		_, _ = fmt.Fprintf(out, "  tags:        %s\n", strings.Join(idea.Tags, ", "))
	}
	if len(idea.Pursuits) > 0 {
		_, _ = fmt.Fprintf(out, "  pursuits:    %s\n", pursuitList(idea.Pursuits))
	}
	if initiative := idea.Initiative; initiative != nil {
		_, _ = fmt.Fprintf(out, "  initiative:  #%d %s (%s)\n", initiative.Number, initiative.Name, initiativeProgress(*initiative))
	}
	if idea.Claim != nil {
		_, _ = fmt.Fprintf(out, "  claim:       %s\n", claimNote(*idea.Claim, now))
	}
	_, _ = fmt.Fprintf(out, "  created:     %s\n", localDay(idea.CreatedTS))
	if description := strValue(idea.Description); description != "" {
		_, _ = fmt.Fprintf(out, "\nDescription:\n%s\n", indent(description, "  "))
	}
	printRefs(out, idea.Refs)
	printRecordParts(out, idea.RecordParts, now)
}
