package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/gotermstyle"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/datapointchris/slate/cli/internal/api"
)

var documentHints = []string{
	"List a record's documents: slate documents list <record>",
}

func newDocumentsCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "documents",
		Short: "Read and write a record's documents, a version at a time",
		Long: "A record of any kind keeps documents, each of one kind: " + strings.Join(api.DocumentKinds, ", ") + ".\n" +
			"Each is named on its record, as 412/plan, and every write adds a version. A\n" +
			"version names who wrote it, the agent session that did, and the worklog that\n" +
			"session was in, so a later go at the record reads which go wrote what. A plan\n" +
			"written in a worklog brings it to its planned stage.\n" +
			"\n" +
			"`show` numbers a version's lines and prints each line comment under its line.\n" +
			"A comment written on another version follows its line into this one while the\n" +
			"line is unchanged. One whose line this version does not hold is printed after\n" +
			"the document. `slate comments add --document --line` comments on a line.",
		Example: "  slate documents show 412 plan                  read 412's plan, its lines numbered\n" +
			"  slate documents write 412 plan --file plan.md  add a version of it\n" +
			"  slate documents list 412                       every document 412 keeps",
	})
	goclikit.WithRecoveryHints(cmd, documentHints...)
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupReading,
		goclikit.WithRecoveryHints(newDocumentsListCommand(), documentHints...),
		goclikit.WithRecoveryHints(newDocumentsShowCommand(), documentHints...),
	)...)
	cmd.AddCommand(inGroup(groupChanging,
		goclikit.WithRecoveryHints(newDocumentsWriteCommand(), documentHints...),
	)...)
	return cmd
}

func newDocumentsListCommand() *cobra.Command {
	var (
		asJSON bool
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "list [<record>]",
		Short: "List documents with their latest version, the most recently written first",
		Long:  "With a record, only that record's documents. COMMENTS counts the line comments on every version.",
		Example: "  slate documents list 412\n" +
			"  slate documents list --limit 10 --json",
		Args:        cobra.MaximumNArgs(1),
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, args []string) error {
			record := ""
			if len(args) == 1 {
				record = args[0]
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			documents, err := client.ListDocuments(cmd.Context(), record, limitFlag(cmd))
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), documents)
			}
			if len(documents) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No documents.")
				return nil
			}
			printDocumentsTable(cmd.OutOrStdout(), documents, time.Now(), "")
			return nil
		},
	}
	addLimitFlag(cmd, &limit)
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output documents as JSON to stdout")
	return cmd
}

func newDocumentsShowCommand() *cobra.Command {
	var (
		asJSON  bool
		version int
	)
	cmd := &cobra.Command{
		Use:   "show <record> <name>",
		Short: "Show a document's latest version, or --version, with its line comments",
		Long: "Lines are numbered as `slate comments add --line` takes them. Each line comment\n" +
			"is printed under its line. One whose line this version does not hold unchanged\n" +
			"is printed after the document, with the line it was written on. Every version\n" +
			"is listed last, with who wrote it and the worklog it was written in.",
		Example: "  slate documents show 412 plan\n" +
			"  slate documents show 412 plan --version 1 --json",
		Args:        cobra.ExactArgs(2),
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, args []string) error {
			var asked *int
			if cmd.Flags().Changed("version") {
				asked = &version
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			document, err := client.GetDocument(cmd.Context(), args[0], args[1], asked)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), document)
			}
			printDocument(cmd.OutOrStdout(), document, time.Now())
			return nil
		},
	}
	cmd.Flags().IntVar(&version, "version", 0, "The version to show (default the latest)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the document as JSON to stdout")
	return cmd
}

func newDocumentsWriteCommand() *cobra.Command {
	var (
		asJSON  bool
		file    string
		kind    string
		session string
	)
	cmd := &cobra.Command{
		Use:   "write <record> <name>",
		Short: "Write a document's next version from a file or stdin, creating it on its first",
		Long: "The body is --file, or stdin when --file is - or absent, kept exactly as given\n" +
			"so its lines number as the file's do. A body matching the latest version writes\n" +
			"nothing and warns.\n" +
			"\n" +
			"--kind is one of " + strings.Join(api.DocumentKinds, ", ") + ", and the write that creates a\n" +
			"document needs it. A name that is a kind holds only that kind, so\n" +
			"`write 412 plan` needs no --kind. A second document of one kind takes another\n" +
			"name, as `write 412 cli-plan --kind plan`.\n" +
			"\n" +
			"The version is written as the login slate runs under, and names the session\n" +
			"and the worklog it is in. A session in no worklog warns, since the version then\n" +
			"names no go at the record.",
		Example: "  slate documents write 412 plan --file plan.md\n" +
			"  slate documents write 412 humanlayer --kind research < research.md",
		Args:        cobra.ExactArgs(2),
		Annotations: scopesForService(scopeDocumentsWrite),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[1]
			if !cmd.Flags().Changed("kind") && slices.Contains(api.DocumentKinds, name) {
				kind = name
			}
			if kind != "" && !slices.Contains(api.DocumentKinds, kind) {
				return goclikit.UsageError(fmt.Errorf("unknown kind %q — one of: %s", kind, strings.Join(api.DocumentKinds, ", ")))
			}
			body, err := readDocumentBody(cmd, file)
			if err != nil {
				return err
			}
			if strings.TrimSpace(body) == "" {
				return goclikit.UsageError(errors.New("the body is blank; a document version holds text"))
			}
			place := agentPlace(session)
			in := api.DocumentWriteInput{Body: body, Kind: nonEmpty(kind), Session: place.Session, Host: place.Host}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.WriteDocument(cmd.Context(), args[0], name, in)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, documentWriteSummary(written))
		},
	}
	f := cmd.Flags()
	f.StringVar(&file, "file", "", "The file holding the body, or - for stdin (default stdin)")
	f.StringVar(&kind, "kind", "", "One of: "+strings.Join(api.DocumentKinds, ", ")+" (default the name, when it is one)")
	f.StringVar(&session, "session", os.Getenv(claudeCodeSessionEnv), agentSessionHelp+", which wrote it")
	f.BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

// readDocumentBody reads the body from the file named, or from stdin. A
// terminal on stdin with no file is refused, since a read would wait on typing.
func readDocumentBody(cmd *cobra.Command, file string) (string, error) {
	if file != "" && file != "-" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return "", goclikit.UsageError(fmt.Errorf("read --file: %w", err))
		}
		return string(raw), nil
	}
	in := cmd.InOrStdin()
	if stdin, ok := in.(*os.File); ok && file == "" && term.IsTerminal(int(stdin.Fd())) {
		return "", goclikit.UsageError(errors.New("pass the body with --file, or pipe it in"))
	}
	raw, err := io.ReadAll(in)
	if err != nil {
		return "", fmt.Errorf("read the body from stdin: %w", err)
	}
	return string(raw), nil
}

// documentWriteSummary names the version written, or that none was.
func documentWriteSummary(written api.DocumentWritten) string {
	document := written.Document
	for _, warning := range written.Warnings {
		if warning.Code == "document_unchanged" {
			return fmt.Sprintf("%s stays at version %d", document.Address, document.Version.Version)
		}
	}
	line := fmt.Sprintf("Wrote %s version %d, %s", document.Address, document.Version.Version, document.Kind)
	if document.Version.Version == 1 {
		line = fmt.Sprintf("Wrote %s, a new %s", document.Address, document.Kind)
	}
	if worklog := strValue(document.Version.Worklog); worklog != "" {
		line += ", in worklog " + worklog
	}
	return line
}

// versionByline says who wrote a version, when, and the worklog it was written in.
func versionByline(version api.DocumentVersionSummary, now time.Time) string {
	line := localClock(version.WrittenTS, now) + " by " + version.WrittenBy
	if worklog := strValue(version.Worklog); worklog != "" {
		line += " in " + worklog
	}
	if session := strValue(version.Session); session != "" {
		line += " (session " + session + ")"
	}
	return line
}

func printDocumentsTable(out io.Writer, documents []api.DocumentSummary, now time.Time, prefix string) {
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, prefix+"DOCUMENT\tKIND\tVERSION\tWRITTEN\tWORKLOG\tCOMMENTS")
	for _, document := range documents {
		latest := document.Latest
		_, _ = fmt.Fprintf(tw, "%s%s\t%s\t%d\t%s\t%s\t%d\n", prefix, document.Address, document.Kind, latest.Version,
			localClock(latest.WrittenTS, now), orDash(strValue(latest.Worklog)), document.CommentCount)
	}
	_ = tw.Flush()
}

func printDocument(out io.Writer, document api.Document, now time.Time) {
	p := gotermstyle.PaletteFor(out)
	shown := document.Version
	_, _ = fmt.Fprintf(out, "%s  %s, version %d of %d  %s %s\n", p.Cyan(document.Address), document.Kind,
		shown.Version, len(document.Versions), p.Cyan("#"+strconv.Itoa(document.Record.Number)), p.Bold(document.Record.Title))
	_, _ = fmt.Fprintf(out, "  written:  %s\n\n", versionByline(shown.DocumentVersionSummary, now))

	under := map[int][]api.PlacedComment{}
	var changed []api.PlacedComment
	for _, comment := range document.Comments {
		if comment.ShownLine == nil {
			changed = append(changed, comment)
			continue
		}
		under[*comment.ShownLine] = append(under[*comment.ShownLine], comment)
	}
	lines := shown.Lines
	width := len(strconv.Itoa(len(lines)))
	pad := strings.Repeat(" ", width+4)
	for i, text := range lines {
		_, _ = fmt.Fprintf(out, "  %*d  %s\n", width, i+1, text)
		for _, comment := range under[i+1] {
			printPlacedComment(out, comment, shown.Version, pad, now)
		}
	}
	if len(changed) > 0 {
		_, _ = fmt.Fprintf(out, "\nComments on lines not in version %d (%d):\n", shown.Version, len(changed))
		for _, comment := range changed {
			printPlacedComment(out, comment, shown.Version, "  ", now)
			_, _ = fmt.Fprintf(out, "      it was on: %s\n", comment.On.Text)
		}
	}
	_, _ = fmt.Fprintf(out, "\nVersions (%d):\n", len(document.Versions))
	for _, version := range document.Versions {
		_, _ = fmt.Fprintf(out, "  %d  %s, %d %s\n", version.Version, versionByline(version, now), version.LineCount,
			plural(version.LineCount, "line", "lines"))
	}
}

// printPlacedComment prints a line comment indented under its line, naming the
// version and line it was written on when that is not the one shown.
func printPlacedComment(out io.Writer, comment api.PlacedComment, shown int, prefix string, now time.Time) {
	line := prefix + "> " + commentByline(comment.Comment, now)
	if on := comment.On; on != nil && on.Version != shown {
		line += fmt.Sprintf("  on version %d line %d", on.Version, on.Line)
	}
	_, _ = fmt.Fprintln(out, line)
	_, _ = fmt.Fprintln(out, indent(comment.Body, prefix+"    "))
}
