package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/datapointchris/slate/cli/internal/api"
)

var commentHints = []string{
	"List a record's comments: slate comments list <record>",
	"List a record's documents: slate documents list <record>",
}

func newCommentsCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "comments",
		Short: "Read and leave comments on any record, or on one line of its document",
		Long: "A comment sits on a record of any kind, or on one line of a version of the\n" +
			"record's document. Each names who wrote it, from the login slate runs under,\n" +
			"and the agent session and worklog it came from. `slate show <record>` prints\n" +
			"the comments on the record, and `slate documents show` prints each line\n" +
			"comment under its line.",
	})
	goclikit.WithRecoveryHints(cmd, commentHints...)
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupReading,
		goclikit.WithRecoveryHints(newCommentsListCommand(), commentHints...),
	)...)
	cmd.AddCommand(inGroup(groupChanging,
		goclikit.WithRecoveryHints(newCommentsAddCommand(), commentHints...),
		goclikit.WithRecoveryHints(newCommentsRemoveCommand(), commentHints...),
	)...)
	return cmd
}

func newCommentsListCommand() *cobra.Command {
	var (
		asJSON   bool
		document string
		limit    int
	)
	cmd := &cobra.Command{
		Use:   "list <record>",
		Short: "List a record's comments, oldest first, line comments included",
		Long: "Each row starts with the comment's id, which `remove` takes. A line comment\n" +
			"names the document version and line it was written on. --document narrows the\n" +
			"list to one document's line comments.",
		Example: "  slate comments list 412\n" +
			"  slate comments list 412 --document plan --json",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			comments, err := client.ListComments(cmd.Context(), args[0], document, limitFlag(cmd))
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
			printComments(cmd.OutOrStdout(), comments, time.Now())
			return nil
		},
	}
	cmd.Flags().StringVar(&document, "document", "", "Only the line comments on this document, by name")
	addLimitFlag(cmd, &limit)
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output comments as JSON to stdout")
	return cmd
}

func newCommentsAddCommand() *cobra.Command {
	var (
		body     string
		document string
		line     int
		version  int
		session  string
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:   "add <record> [<text>]",
		Short: "Comment on a record, or on one line of its document",
		Long: "Progress, a handoff, feedback on a plan, or what completing an issue shipped.\n" +
			"The comment is markdown, given after the record or in --body. One that opens\n" +
			"with - goes after --.\n" +
			"\n" +
			"--document and --line put it on one line of that document's latest version,\n" +
			"or of --version. The line is the number `slate documents show` prints beside\n" +
			"it. The comment is written as the login slate runs under, and names the\n" +
			"session and the worklog it is in.",
		Example: "  slate comments add 412 \"Root cause: the router order\"\n" +
			"  slate comments add 412 --document plan --line 12 \"Split this step in two\"\n" +
			"  slate comments add 412 -- \"- shipped the rules\"\n" +
			"  slate comments add 412 --body \"$(cat handoff.md)\"",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 2 {
				if cmd.Flags().Changed("body") {
					return goclikit.UsageError(errors.New("give the comment once — after the record, or in --body"))
				}
				body = args[1]
			}
			if strings.TrimSpace(body) == "" {
				return goclikit.UsageError(errors.New("the comment is required and cannot be blank — give it after the record, or in --body"))
			}
			on := cmd.Flags().Changed("document")
			if on != cmd.Flags().Changed("line") {
				return goclikit.UsageError(errors.New("a line comment names both --document and --line"))
			}
			if cmd.Flags().Changed("version") && !on {
				return goclikit.UsageError(errors.New("--version names a version of --document, so it needs --document and --line"))
			}
			in := api.CommentAddInput{Record: args[0], Body: body}
			if on {
				in.Document, in.Line = &document, &line
				if cmd.Flags().Changed("version") {
					in.Version = &version
				}
			}
			place := agentPlace(session)
			in.Session, in.Host = place.Session, place.Host
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.AddComment(cmd.Context(), in)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, "Commented "+commentPlace(written.Comment))
		},
	}
	f := cmd.Flags()
	f.StringVar(&body, "body", "", "The comment, as markdown, in place of the text after the record")
	f.StringVar(&document, "document", "", "The document whose line it is on, by name")
	f.IntVar(&line, "line", 0, "The line it is on, counting from one")
	f.IntVar(&version, "version", 0, "The version whose line it is (default the latest)")
	f.StringVar(&session, "session", os.Getenv(claudeCodeSessionEnv), agentSessionHelp+", which wrote it")
	f.BoolVar(&asJSON, "json", false, writtenJSONHelp)
	// A comment that opens with a bullet parses as a group of short flags.
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		var unknown *pflag.NotExistError
		if errors.As(err, &unknown) && unknown.GetSpecifiedShortnames() != "" {
			err = fmt.Errorf("%w — a comment that opens with - goes after --, as in: slate comments add 412 -- \"- shipped\"", err)
		}
		return goclikit.UsageError(err)
	})
	return cmd
}

func newCommentsRemoveCommand() *cobra.Command {
	var (
		asJSON bool
		yes    bool
	)
	cmd := &cobra.Command{
		Use:     "remove <comment>",
		Short:   "Delete one comment",
		Long:    "The comment is its id, the number that starts its row in `slate comments list`.",
		Example: "  slate comments remove 57",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.Atoi(strings.TrimPrefix(args[0], "#"))
			if err != nil || id < 1 {
				return goclikit.UsageError(fmt.Errorf("comment %q: pass its id from `slate comments list <record>`", args[0]))
			}
			if !yes {
				ok, err := confirm(cmd, fmt.Sprintf("Delete comment %d?", id))
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
			deleted, err := client.DeleteComment(cmd.Context(), id)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			summary := fmt.Sprintf("Deleted comment %d %s:\n%s", id, commentPlace(deleted.Deleted), indent(deleted.Deleted.Body, "  "))
			return reportWrite(cmd, asJSON, deleted, deleted.WriteEffects, summary)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

// commentPlace says where a comment sits and the worklog it was written in.
func commentPlace(comment api.Comment) string {
	place := fmt.Sprintf("on #%d", comment.RecordNumber)
	if on := comment.On; on != nil {
		place = fmt.Sprintf("on %d/%s v%d line %d", comment.RecordNumber, on.Document, on.Version, on.Line)
	}
	if worklog := strValue(comment.Worklog); worklog != "" {
		place += " in worklog " + worklog
	}
	return place
}

// commentByline is a comment's id, when it was written, by whom, and where from.
func commentByline(comment api.Comment, now time.Time) string {
	line := fmt.Sprintf("%d  %s  %s", comment.ID, localClock(comment.WrittenTS, now), orDash(strValue(comment.WrittenBy)))
	if worklog := strValue(comment.Worklog); worklog != "" {
		line += "  in " + worklog
	} else if session := strValue(comment.Session); session != "" {
		line += "  session " + session
	}
	return line
}

func printComments(out io.Writer, comments []api.Comment, now time.Time) {
	for i, comment := range comments {
		if i > 0 {
			_, _ = fmt.Fprintln(out)
		}
		line := "  " + commentByline(comment, now)
		if on := comment.On; on != nil {
			line += fmt.Sprintf("  on %s v%d line %d", on.Document, on.Version, on.Line)
		}
		_, _ = fmt.Fprintln(out, line)
		_, _ = fmt.Fprintln(out, indent(comment.Body, "      "))
	}
}

// printRecordDocumentsAndComments closes a record's read with its documents
// and the comments on it, as every kind's show prints them.
func printRecordDocumentsAndComments(out io.Writer, documents []api.DocumentSummary, comments []api.Comment, now time.Time) {
	if len(documents) > 0 {
		_, _ = fmt.Fprintf(out, "\nDocuments (%d):\n", len(documents))
		printDocumentsTable(out, documents, now, "  ")
	}
	if len(comments) > 0 {
		_, _ = fmt.Fprintf(out, "\nComments (%d):\n", len(comments))
		printComments(out, comments, now)
	}
}
