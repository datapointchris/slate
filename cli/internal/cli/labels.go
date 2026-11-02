package cli

import (
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

var labelHints = []string{"List the labels: slate labels list"}

func newLabelsCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "labels",
		Short: "Manage the labels an issue's --label accepts",
		Long: "An issue carries only labels made here, so a typo is refused rather than\n" +
			"becoming a label nobody filters on. An issue carries at most one label from\n" +
			"each group, so area-api and area-cli, both in the area group, never meet on\n" +
			"one issue.",
	})
	goclikit.WithRecoveryHints(cmd, labelHints...)
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupReading,
		newLabelsListCommand(),
		newLabelsShowCommand(),
	)...)
	cmd.AddCommand(inGroup(groupChanging,
		newLabelsCreateCommand(),
		newLabelsEditCommand(),
		newLabelsDeleteCommand(),
	)...)
	return cmd
}

func newLabelsListCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:         "list",
		Short:       "List every label, grouped, with its count of unclosed issues",
		Example:     "  slate labels list\n  slate labels list --json",
		Args:        cobra.NoArgs,
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			labels, err := client.ListIssueLabels(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), labels)
			}
			printIssueLabels(cmd.OutOrStdout(), labels)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output labels as JSON to stdout")
	return cmd
}

func newLabelsShowCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:         "show <label>",
		Short:       "Show one label",
		Example:     "  slate labels show area-api",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			label, err := client.GetIssueLabel(cmd.Context(), args[0])
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), label)
			}
			printIssueLabels(cmd.OutOrStdout(), []api.IssueLabel{label})
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the label as JSON to stdout")
	return cmd
}

func newLabelsCreateCommand() *cobra.Command {
	var (
		slug        string
		group       string
		description string
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "create --slug <slug> [flags]",
		Short: "Add a label to the vocabulary",
		Long: "A slug is lowercase words joined by hyphens. --group makes it exclusive with\n" +
			"the other labels in that group.",
		Example: "  slate labels create --slug area-db --group area\n" +
			"  slate labels create --slug needs-design --description \"Waiting on a design call\"",
		Args:        cobra.NoArgs,
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if slug == "" {
				return goclikit.UsageError(errors.New("--slug is required"))
			}
			f := cmd.Flags()
			in := api.IssueLabelCreateInput{
				Slug:        slug,
				GroupSlug:   valuedStrFlag(f, "group", &group),
				Description: valuedStrFlag(f, "description", &description),
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.CreateIssueLabel(cmd.Context(), in)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, "Added label "+written.Label.Slug)
		},
	}
	cmd.Flags().StringVar(&slug, "slug", "", "The label, as lowercase-hyphenated words (required)")
	cmd.Flags().StringVar(&group, "group", "", "Group it is exclusive within")
	cmd.Flags().StringVar(&description, "description", "", "When the label applies")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newLabelsEditCommand() *cobra.Command {
	var (
		group       string
		description string
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "edit <label> [flags]",
		Short: "Change a label's group or description",
		Long: "A label's slug is its name on every issue carrying it, so it does not change.\n" +
			"An empty value clears a field. Moving a label into a group is refused while an\n" +
			"issue carries it beside another label of that group.",
		Example:     "  slate labels edit area-db --group area\n  slate labels edit area-db --group \"\"",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			in := api.IssueLabelUpdateInput{
				GroupSlug:   valuedStrFlag(f, "group", &group),
				Description: valuedStrFlag(f, "description", &description),
			}
			var clear []string
			if readFlagIntent(f, "group") == flagClears {
				clear = append(clear, "group_slug")
			}
			if readFlagIntent(f, "description") == flagClears {
				clear = append(clear, "description")
			}
			if in == (api.IssueLabelUpdateInput{}) && len(clear) == 0 {
				return goclikit.UsageError(errors.New("nothing to change — pass --group and/or --description"))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.UpdateIssueLabel(cmd.Context(), args[0], in, clear)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, "Updated label "+written.Label.Slug)
		},
	}
	cmd.Flags().StringVar(&group, "group", "", "Group it is exclusive within (empty removes it from its group)")
	cmd.Flags().StringVar(&description, "description", "", "When the label applies (empty clears)")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newLabelsDeleteCommand() *cobra.Command {
	var (
		yes    bool
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "delete <label> [--yes]",
		Short: "Remove a label from the vocabulary and from every issue",
		Long: "Every issue carrying it loses it, closed ones included, and each is printed.\n" +
			"Putting it back means creating it and labeling each issue again, so it\n" +
			"prompts unless --yes.",
		Example:     "  slate labels delete area-db --yes",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			label, err := client.GetIssueLabel(cmd.Context(), args[0])
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if !yes {
				question := fmt.Sprintf("Delete label %s? %d unclosed %s carry it.",
					label.Slug, label.OpenIssueCount, plural(label.OpenIssueCount, "issue", "issues"))
				ok, err := confirm(cmd, question)
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Aborted.")
					return nil
				}
			}
			deleted, err := client.DeleteIssueLabel(cmd.Context(), label.Slug)
			if err != nil {
				return handleAPIError(err)
			}
			return reportWrite(cmd, asJSON, deleted, deleted.WriteEffects, "Deleted label "+label.Slug)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func printIssueLabels(out io.Writer, labels []api.IssueLabel) {
	if len(labels) == 0 {
		_, _ = fmt.Fprintln(out, "No labels. Add one with `slate labels create --slug ...`.")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "LABEL\tGROUP\tOPEN\tDESCRIPTION")
	for _, label := range labels {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", label.Slug, orDash(strValue(label.GroupSlug)), label.OpenIssueCount, strValue(label.Description))
	}
	_ = tw.Flush()
}
