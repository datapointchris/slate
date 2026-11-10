package cli

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/datapointchris/slate/cli/internal/api"
)

var tagHints = []string{"List the tags: slate tags list"}

// The help each kind's --tag carries, on a create, an edit and a list.
const (
	tagSetHelp     = "Tag to carry (repeatable; see: slate tags list)"
	tagReplaceHelp = "Tag (repeatable; replaces the set, empty removes every tag; see: slate tags list)"
	tagFilterHelp  = "Only records carrying this tag, an issue also through its initiative (see: slate tags list)"
)

// tagsEdited reads an edit's --tag: nil where it was not passed, an empty set
// where it was passed empty, and the values otherwise.
func tagsEdited(f *pflag.FlagSet, values []string) (*[]string, error) {
	switch readFlagIntent(f, "tag") {
	case flagClears:
		return &[]string{}, nil
	case flagCarriesValues:
		return &values, nil
	case flagMixesEmptyWithValues:
		return nil, goclikit.UsageError(errors.New("--tag given both a value and an empty one — an empty value removes every tag, so pass one or the other"))
	case flagUntouched:
	}
	return nil, nil
}

// honoredTagWrite refuses a write whose answer carries other tags than the
// --tag it was sent, as an API from before tags drops it and answers as though
// it held it. A nil want sent none, so there is nothing to check.
func honoredTagWrite(want *[]string, number int, carried []string) error {
	if want == nil || sameTags(*want, carried) {
		return nil
	}
	return fmt.Errorf("the API ignored --tag: #%d was written carrying %s rather than %s, so it predates tags",
		number, tagsOrNone(carried), tagsOrNone(*want))
}

func sameTags(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(slices.Compact(x), slices.Compact(y))
}

func tagsOrNone(tags []string) string {
	if len(tags) == 0 {
		return "no tag"
	}
	return strings.Join(tags, ", ")
}

func newTagsCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "tags",
		Short: "Manage the tags every kind's --tag accepts",
		Long: "A tag is a category with no end, such as security or data-quality, that ideas,\n" +
			"initiatives and issues carry. A record carries only a tag made here, so perf\n" +
			"where performance exists is refused rather than becoming a second tag. An issue\n" +
			"reads as carrying its initiative's tags too, so --tag on an initiative reaches\n" +
			"its issues.",
		Example: "  slate tags list                                             every tag with its open records\n" +
			"  slate tags create security --description \"What an attacker reaches\"\n" +
			"  slate tags rename perf performance",
	})
	goclikit.WithRecoveryHints(cmd, tagHints...)
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupReading,
		newTagsListCommand(),
	)...)
	cmd.AddCommand(inGroup(groupChanging,
		newTagsCreateCommand(),
		newTagsEditCommand(),
		newTagsRenameCommand(),
		newTagsDeleteCommand(),
	)...)
	return cmd
}

func newTagsListCommand() *cobra.Command {
	var (
		asJSON bool
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every tag with its description and its count of open records",
		Long: "OPEN counts the open ideas, initiatives and issues carrying the tag themselves.\n" +
			"An issue reading its initiative's tag is counted on the initiative.",
		Example:     "  slate tags list\n  slate tags list --json",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			tags, err := client.ListTags(cmd.Context(), limitFlag(cmd))
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), tags)
			}
			printTags(cmd.OutOrStdout(), tags)
			return nil
		},
	}
	addLimitFlag(cmd, &limit)
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output tags as JSON to stdout")
	return cmd
}

func newTagsCreateCommand() *cobra.Command {
	var (
		description string
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:   "create <tag> --description <line>",
		Short: "Add a tag to the list",
		Long: "A tag is lowercase words joined by hyphens. --description says when it applies,\n" +
			"on one line, and is required.",
		Example:     "  slate tags create data-quality --description \"Whether the data a tool records is right\"",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(description) == "" {
				return goclikit.UsageError(errors.New("--description is required — say when the tag applies, on one line"))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.CreateTag(cmd.Context(), api.TagCreateInput{Name: args[0], Description: description})
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, "Added tag "+written.Tag.Name)
		},
	}
	cmd.Flags().StringVar(&description, "description", "", "When the tag applies, on one line (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newTagsEditCommand() *cobra.Command {
	var (
		description string
		asJSON      bool
	)
	cmd := &cobra.Command{
		Use:         "edit <tag> --description <line>",
		Short:       "Change a tag's description",
		Long:        "A tag always has a description, so it cannot be emptied. `slate tags rename` changes the name.",
		Example:     "  slate tags edit security --description \"Credentials, roles and what an attacker reaches\"",
		SuggestFor:  editMistakenFor,
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch readFlagIntent(cmd.Flags(), "description") {
			case flagUntouched:
				return goclikit.UsageError(errors.New("nothing to change — pass --description"))
			case flagClears, flagMixesEmptyWithValues:
				return goclikit.UsageError(errors.New("--description cannot be emptied — every tag carries one"))
			case flagCarriesValues:
			}
			return runTagUpdate(cmd, args[0], api.TagUpdateInput{Description: &description}, asJSON, "Updated tag ")
		},
	}
	cmd.Flags().StringVar(&description, "description", "", "When the tag applies, on one line")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newTagsRenameCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:         "rename <tag> <new-name>",
		Short:       "Rename a tag on every record carrying it",
		Long:        "Every record carrying the tag carries it under the new name. A name another tag holds is refused.",
		Example:     "  slate tags rename perf performance",
		Args:        cobra.ExactArgs(2),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTagUpdate(cmd, args[0], api.TagUpdateInput{Name: &args[1]}, asJSON, "Renamed "+args[0]+" to ")
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func runTagUpdate(cmd *cobra.Command, name string, in api.TagUpdateInput, asJSON bool, verb string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return handleAPIError(err)
	}
	written, err := client.UpdateTag(cmd.Context(), name, in)
	if err != nil {
		return handleArgumentAPIError(err)
	}
	return reportWrite(cmd, asJSON, written, written.WriteEffects, verb+written.Tag.Name)
}

func newTagsDeleteCommand() *cobra.Command {
	var (
		yes    bool
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "delete <tag> [--yes]",
		Short: "Remove a tag from the list and from every record carrying it",
		Long: "Every record carrying it loses it, closed ones included, and each is printed.\n" +
			"Putting it back means creating it and tagging each record again, so it\n" +
			"prompts unless --yes.",
		Example:     "  slate tags delete perf --yes",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			tag, err := client.GetTag(cmd.Context(), args[0])
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if !yes {
				question := fmt.Sprintf("Delete tag %s? %d open %s carry it.",
					tag.Name, tag.OpenRecordCount, plural(tag.OpenRecordCount, "record", "records"))
				ok, err := confirm(cmd, question)
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Aborted.")
					return nil
				}
			}
			deleted, err := client.DeleteTag(cmd.Context(), tag.Name)
			if err != nil {
				return handleAPIError(err)
			}
			return reportWrite(cmd, asJSON, deleted, deleted.WriteEffects, "Deleted tag "+tag.Name)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func printTags(out io.Writer, tags []api.Tag) {
	if len(tags) == 0 {
		_, _ = fmt.Fprintln(out, "No tags. Add one with `slate tags create <tag> --description ...`.")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "TAG\tOPEN\tIDEAS\tINITIATIVES\tISSUES\tDESCRIPTION")
	for _, tag := range tags {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%s\n",
			tag.Name, tag.OpenRecordCount, tag.OpenIdeaCount, tag.OpenInitiativeCount, tag.OpenIssueCount, tag.Description)
	}
	_ = tw.Flush()
}

// issueTagsLine is an issue's tags, each it reads only through its initiative
// marked with that initiative's number.
func issueTagsLine(issue api.Issue) string {
	own := map[string]bool{}
	for _, tag := range issue.Tags {
		own[tag] = true
	}
	shown := make([]string, 0, len(issue.EffectiveTags))
	for _, tag := range issue.EffectiveTags {
		if !own[tag] && issue.Initiative != nil {
			tag += fmt.Sprintf(" (from %d)", issue.Initiative.Number)
		}
		shown = append(shown, tag)
	}
	return strings.Join(shown, ", ")
}
