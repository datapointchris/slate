package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

const (
	refSetHelp    = "A key or URL that it came from, such as capture/<id> (repeatable)"
	refFilterHelp = "Only records carrying a ref that starts with this, such as capture/"
)

// refsLinked is what a link wrote: the answer, its effects, the record's line,
// and the count of refs on the record now.
type refsLinked struct {
	written any
	effects api.WriteEffects
	label   string
	count   int
}

// refLinkWrites is every kind that carries refs, and how a ref is linked to one.
var refLinkWrites = map[string]func(ctx context.Context, client *api.Client, ref string, in api.RecordRefsInput) (refsLinked, error){
	"issue": func(ctx context.Context, client *api.Client, ref string, in api.RecordRefsInput) (refsLinked, error) {
		written, err := client.LinkIssueRefs(ctx, ref, in, issueZone())
		issue := written.Issue
		return refsLinked{written, written.WriteEffects, fmt.Sprintf("Issue #%d %q", issue.Number, issue.Title), len(issue.Refs)}, err
	},
	"initiative": func(ctx context.Context, client *api.Client, ref string, in api.RecordRefsInput) (refsLinked, error) {
		written, err := client.LinkInitiativeRefs(ctx, ref, in)
		initiative := written.Initiative
		return refsLinked{written, written.WriteEffects, fmt.Sprintf("Initiative #%d %q", initiative.Number, initiative.Name), len(initiative.Refs)}, err
	},
	"idea": func(ctx context.Context, client *api.Client, ref string, in api.RecordRefsInput) (refsLinked, error) {
		written, err := client.LinkIdeaRefs(ctx, ref, in)
		idea := written.Idea
		return refsLinked{written, written.WriteEffects, fmt.Sprintf("Idea #%d %q", idea.Number, idea.Title), len(idea.Refs)}, err
	},
}

func newRefsLinkCommand(kind, example string) *cobra.Command {
	var (
		note     string
		linkedBy string
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:   "link <" + kind + "> <ref>...",
		Short: "Link what an " + kind + " came from or rests on, by key or URL",
		Long: "Each ref is a key that the system holding the thing gave it, such as an inbox\n" +
			"row's capture/<id>, or an http or https URL. slate stores it as given and never\n" +
			"follows it. A ref that the record already carries is left as it was, with a\n" +
			"warning on stderr. --note is kept on every ref this call links. `list --ref`\n" +
			"finds every record carrying a ref by its prefix.\n" +
			"\n" +
			"--linked-by defaults to who is running slate: the Claude Code session, the\n" +
			"worker $" + actorWorkerEnv + " names, or user@host.",
		Example:     example,
		Args:        cobra.MinimumNArgs(2),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(linkedBy) == "" {
				return goclikit.UsageError(errors.New("--linked-by cannot be empty — a link names who made it"))
			}
			refs := args[1:]
			if err := refuseBlankRef(refs); err != nil {
				return err
			}
			in := api.RecordRefsInput{Refs: refs, Note: valuedStrFlag(cmd.Flags(), "note", &note), LinkedBy: &linkedBy}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			linked, err := refLinkWrites[kind](cmd.Context(), client, args[0], in)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			summary := fmt.Sprintf("%s carries %d %s", linked.label, linked.count, plural(linked.count, "ref", "refs"))
			return reportWrite(cmd, asJSON, linked.written, linked.effects, summary)
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "What the refs show")
	cmd.Flags().StringVar(&linkedBy, "linked-by", actorIdentity(), "Who linked them")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func issueRefs(issue api.Issue) (int, []api.RecordRef) { return issue.Number, issue.Refs }

func initiativeRefs(initiative api.Initiative) (int, []api.RecordRef) {
	return initiative.Number, initiative.Refs
}

func ideaRefs(idea api.Idea) (int, []api.RecordRef) { return idea.Number, idea.Refs }

// refuseBlankRef refuses a ref that names nothing, before the API does.
func refuseBlankRef(refs []string) error {
	if i := slices.IndexFunc(refs, func(ref string) bool { return strings.TrimSpace(ref) == "" }); i >= 0 {
		return goclikit.UsageError(fmt.Errorf("ref %d is blank — every ref names what it points at", i+1))
	}
	return nil
}

// honoredRefFilter refuses a reply in which a row carries no ref starting
// with the --ref prefix it was narrowed to. An API from before refs drops the
// filter and answers every record, so a caller asking whether any record names
// its key would read every one as naming it.
func honoredRefFilter[T any](prefix string, rows []T, of func(T) (int, []api.RecordRef)) error {
	if prefix == "" {
		return nil
	}
	for _, row := range rows {
		number, refs := of(row)
		if !slices.ContainsFunc(refs, func(ref api.RecordRef) bool { return strings.HasPrefix(ref.Ref, prefix) }) {
			return fmt.Errorf("the API ignored --ref: #%d came back carrying no ref that starts with %s, so it predates refs", number, prefix)
		}
	}
	return nil
}

// honoredRefWrite refuses a create whose answer lacks a --ref it was sent, as
// an API from before refs drops the field and files the record without it.
func honoredRefWrite(want []string, number int, carried []api.RecordRef) error {
	for _, ref := range want {
		if !slices.ContainsFunc(carried, func(c api.RecordRef) bool { return c.Ref == strings.TrimSpace(ref) }) {
			return fmt.Errorf("the API ignored --ref: #%d was written without %s, so it predates refs", number, ref)
		}
	}
	return nil
}

// printRefs prints a record's refs under its detail, oldest first, each with its note.
func printRefs(out io.Writer, refs []api.RecordRef) {
	if len(refs) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out, "\nRefs (%d):\n", len(refs))
	for _, ref := range refs {
		line := "  " + ref.Ref
		if note := strValue(ref.Note); note != "" {
			line += "  " + note
		}
		_, _ = fmt.Fprintln(out, line)
	}
}
