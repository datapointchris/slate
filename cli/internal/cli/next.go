package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/datapointchris/slate/cli/internal/api"
)

// defaultNextIssueLimit caps `next`. The head of the queue is the answer, and
// the rows after it are context.
const defaultNextIssueLimit = 10

func newNextCommand() *cobra.Command {
	var (
		asJSON bool
		queue  readyQueueFlags
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "next",
		Short: "The ready issues, in the order to take them",
		Long: "Open, unblocked, not deferred past today, with no open children, and not held\n" +
			"by a live claim. The first row is what `slate claims hold --next` would take,\n" +
			"under the same flags. Decisions wait on a person and are left out unless\n" +
			"--type decision asks for them. --autofixable keeps only the issues whose own,\n" +
			"initiative's and parents' autofix are all on, the queue an unattended fix reads.\n" +
			"Reading the queue holds nothing, so hold the issue you take before working it.\n" +
			"\n" +
			"The priority column is the one the queue sorts by, and a * marks one inherited\n" +
			"from what the issue blocks, its parent, or its initiative.",
		Example: "  slate next\n" +
			"  slate next --repo webapp --limit 1\n" +
			"  slate next --label area-cli --json\n" +
			"  slate next --autofixable --json\n" +
			"  slate next --type decision",
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

// readyQueueFlags narrow the ready queue, on `next` and `claims hold --next`.
type readyQueueFlags struct {
	repo        string
	issueType   string
	label       string
	initiative  string
	autofixable bool
}

// readyQueueFlagNames are refused by a hold that names its record.
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
	withFlagValues(cmd, "type", api.IssueTypes...)
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
