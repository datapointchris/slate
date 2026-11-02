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

	"github.com/datapointchris/slate/cli/internal/api"
)

var ideaHints = []string{anyRecordHint, "List every idea: slate ideas list"}

// promoteStatuses are where a promoted idea's initiative may start.
var promoteStatuses = []string{"active", "someday"}

func newIdeasCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "ideas",
		Short: "Keep the work nobody has taken on, with its priority and evidence",
		Long: "An idea is work nobody has taken on yet. It carries its own priority, like any\n" +
			"record, and links the evidence it rests on by key or URL. slate stores each\n" +
			"ref as given and never follows it, so a key from any system works.\n" +
			"\n" +
			"An idea is never work, so it has no status. `promote` creates the initiative\n" +
			"that is the work. The initiative names the idea, and the idea then shows the\n" +
			"initiative and its progress. An idea shares its number sequence with\n" +
			"initiatives and issues.",
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
		goclikit.WithRecoveryHints(newIdeasLinkCommand(), ideaHints...),
		goclikit.WithRecoveryHints(newIdeasPromoteCommand(), ideaHints...),
		goclikit.WithRecoveryHints(newIdeasReorderCommand(), ideaHints...),
	)...)
	return cmd
}

func newIdeasReorderCommand() *cobra.Command {
	return newReorderCommand("idea",
		"`slate next` lists an idea at step discuss until it is promoted.",
		"  slate ideas reorder 1530 --before 1491",
		func(cmd *cobra.Command, client *api.Client, ref string, move api.RankMove) (any, api.WriteEffects, string, error) {
			written, err := client.RankIdea(cmd.Context(), ref, move, issueZone())
			return written, written.WriteEffects, fmt.Sprintf("idea #%d %s", written.Idea.Number, written.Idea.Title), err
		})
}

func newIdeasListCommand() *cobra.Command {
	var (
		asJSON bool
		limit  int
	)
	cmd := &cobra.Command{
		Use:         "list",
		Short:       "List every idea, promoted or not, most urgent first",
		Long:        "Priority none sorts after every level. INITIATIVE is the one an idea was promoted into.",
		Example:     "  slate ideas list\n  slate ideas list --json",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			ideas, err := client.ListIdeas(cmd.Context(), limitFlag(cmd))
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), ideas)
			}
			printIdeasTable(cmd.OutOrStdout(), ideas)
			return nil
		},
	}
	addLimitFlag(cmd, &limit)
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
		Short:       "Show an idea with its evidence, its claims, the initiative it became, and its changes",
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
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:         "create --title <title> [flags]",
		Short:       "Record an idea",
		Example:     "  slate ideas create --title \"Run review rounds on a schedule\" --priority high",
		Args:        cobra.NoArgs,
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(title) == "" {
				return goclikit.UsageError(errors.New("--title is required"))
			}
			f := cmd.Flags()
			in := api.IdeaCreateInput{Title: title, Description: valuedStrFlag(f, "description", &description)}
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
			written, err := client.CreateIdea(cmd.Context(), in)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, ideaLine("Recorded", written.Idea))
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "What the idea is, in a line (required)")
	cmd.Flags().StringVar(&description, "description", "", "The idea in full")
	cmd.Flags().StringVar(&priority, "priority", "", "One of: "+priorityChoices()+" (default none)")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newIdeasEditCommand() *cobra.Command {
	var (
		title       string
		description string
		priority    string
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:         "edit <idea> [flags]",
		Short:       "Change an idea's title, description or priority",
		Long:        "Update only the fields whose flags you pass. --description \"\" clears it.",
		Example:     "  slate ideas edit 1860 --priority urgent",
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
			var clear []string
			if readFlagIntent(f, "description") == flagClears {
				clear = append(clear, "description")
			}
			if in == (api.IdeaUpdateInput{}) && len(clear) == 0 {
				return goclikit.UsageError(errors.New("nothing to change — pass at least one of --title/--description/--priority"))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.UpdateIdea(cmd.Context(), args[0], in, clear)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, ideaLine("Updated", written.Idea))
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "New title")
	cmd.Flags().StringVar(&description, "description", "", "New description (empty clears)")
	cmd.Flags().StringVar(&priority, "priority", "", "One of: "+priorityChoices())
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newIdeasLinkCommand() *cobra.Command {
	var (
		note     string
		linkedBy string
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:   "link <idea> <ref>...",
		Short: "Link evidence to an idea, by key or URL",
		Long: "Each ref is a key the system holding the evidence gave it, such as an inbox\n" +
			"row's, or an http or https URL. slate stores it as given and never follows it.\n" +
			"A ref the idea already links is left as it was, with a warning on stderr.\n" +
			"--note is kept on every ref this call links.\n" +
			"\n" +
			"--linked-by defaults to who is running slate: the Claude Code session, the\n" +
			"worker $" + actorWorkerEnv + " names, or user@host.",
		Example: "  slate ideas link 1860 inbox:4102 inbox:4117\n" +
			"  slate ideas link 1860 https://example.com/thread/9 --note \"Where it came up\"",
		Args:        cobra.MinimumNArgs(2),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(linkedBy) == "" {
				return goclikit.UsageError(errors.New("--linked-by cannot be empty — a link names who made it"))
			}
			refs := args[1:]
			if i := slices.IndexFunc(refs, func(ref string) bool { return strings.TrimSpace(ref) == "" }); i >= 0 {
				return goclikit.UsageError(fmt.Errorf("ref %d is blank — every ref names its evidence", i+1))
			}
			in := api.IdeaEvidenceInput{Refs: refs, Note: valuedStrFlag(cmd.Flags(), "note", &note), LinkedBy: &linkedBy}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.LinkIdeaEvidence(cmd.Context(), args[0], in)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			idea := written.Idea
			summary := fmt.Sprintf("Idea #%d %q links %d %s", idea.Number, idea.Title, len(idea.Evidence), plural(len(idea.Evidence), "ref", "refs"))
			return reportWrite(cmd, asJSON, written, written.WriteEffects, summary)
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "What the evidence shows")
	cmd.Flags().StringVar(&linkedBy, "linked-by", actorIdentity(), "Who linked it")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newIdeasPromoteCommand() *cobra.Command {
	var (
		name        string
		description string
		priority    string
		status      string
		autofix     bool
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "promote <idea> [flags]",
		Short: "Create the initiative an idea becomes",
		Long: "Creates an initiative that names the idea, and prints it under the line naming\n" +
			"the promotion. Each flag left out takes the idea's own: its title as the name,\n" +
			"its description, its priority. The idea's claim ends as promoted, printed\n" +
			"under the line too, since its work is the initiative's now. An idea already\n" +
			"promoted is refused, naming its initiative. --status someday creates it set\n" +
			"aside.",
		Example: "  slate ideas promote 1860\n" +
			"  slate ideas promote 1860 --name \"Scheduled review rounds\" --status someday",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			if f.Changed("status") && !slices.Contains(promoteStatuses, status) {
				return goclikit.UsageError(fmt.Errorf("unknown status %q — one of: %s", status, strings.Join(promoteStatuses, ", ")))
			}
			if readFlagIntent(f, "name") == flagClears {
				return goclikit.UsageError(errors.New("--name cannot be empty — leave it out to take the idea's title"))
			}
			in := api.IdeaPromoteInput{
				Name:        valuedStrFlag(f, "name", &name),
				Description: valuedStrFlag(f, "description", &description),
				Status:      status,
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
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.PromoteIdea(cmd.Context(), args[0], in, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, ideaLine("Promoted", written.Idea))
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "The initiative's name (default the idea's title)")
	cmd.Flags().StringVar(&description, "description", "", "The initiative's outcome (default the idea's description)")
	cmd.Flags().StringVar(&priority, "priority", "", "One of: "+priorityChoices()+" (default the idea's)")
	cmd.Flags().StringVar(&status, "status", "", "One of: "+strings.Join(promoteStatuses, ", ")+" (default active)")
	cmd.Flags().BoolVar(&autofix, "autofix", true, initiativeAutofixHelp)
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
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
	_, _ = fmt.Fprintln(tw, "IDEA\tPRIORITY\tEVIDENCE\tINITIATIVE\tTITLE")
	for _, idea := range ideas {
		initiative := "—"
		if idea.Initiative != nil {
			initiative = "#" + strconv.Itoa(idea.Initiative.Number)
		}
		_, _ = fmt.Fprintf(tw, "#%d\t%s\t%d\t%s\t%s\n", idea.Number, priorityName(idea.Priority), len(idea.Evidence), initiative, idea.Title)
	}
	_ = tw.Flush()
}

func printIdeaDetail(out io.Writer, idea api.IdeaDetail, now time.Time) {
	p := gotermstyle.PaletteFor(out)
	_, _ = fmt.Fprintf(out, "%s  %s\n", p.Cyan("#"+strconv.Itoa(idea.Number)), p.Bold(idea.Title))
	_, _ = fmt.Fprintf(out, "  priority:    %s\n", tierColor(p, idea.Priority)(priorityName(idea.Priority)))
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
	if len(idea.Evidence) > 0 {
		_, _ = fmt.Fprintf(out, "\nEvidence (%d):\n", len(idea.Evidence))
		for _, evidence := range idea.Evidence {
			line := "  " + evidence.Ref
			if note := strValue(evidence.Note); note != "" {
				line += "  " + note
			}
			_, _ = fmt.Fprintln(out, line)
		}
	}
	printRecordParts(out, idea.RecordParts, now)
}
