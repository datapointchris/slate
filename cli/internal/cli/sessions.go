package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/gotermstyle"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/datapointchris/slate/cli/internal/api"
	"github.com/datapointchris/slate/cli/internal/transcript"
)

var usageNotReplaced = []string{"usage_unchanged", "usage_stale"}

var sessionHints = []string{
	"A session slate has not seen appears once it reports: slate sessions report --transcript <path>",
	"Show what a session spent: slate sessions show <session>",
}

func newSessionsCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "sessions",
		Short: "Report and read the tokens each agent session spent, split across the worklogs it joined",
		Long: "A session's tokens are counted by kind: uncached input, cache write, cache\n" +
			"read and output. `report` reads a Claude Code session's transcript and its\n" +
			"subagents', and sends each model message's tokens. slate puts each message in\n" +
			"the worklog span the session was in when it was written, and keeps the rest as\n" +
			"the session's alone. A report replaces the one before, so a session reports at\n" +
			"every turn and at its end and counts nothing twice.\n" +
			"\n" +
			"`show` prints a session's tokens in all, in each worklog, and outside them.\n" +
			"`slate worklogs show` prints a worklog's.",
	})
	goclikit.WithRecoveryHints(cmd, sessionHints...)
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupReading, goclikit.WithRecoveryHints(newSessionsShowCommand(), sessionHints...))...)
	cmd.AddCommand(inGroup(groupChanging, newSessionsReportCommand())...)
	return cmd
}

// hookEvent is what Claude Code hands a hook on stdin that report reads.
type hookEvent struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

func newSessionsReportCommand() *cobra.Command {
	var (
		asJSON bool
		path   string
	)
	cmd := &cobra.Command{
		Use:   "report [--transcript <path>]",
		Short: "Report a session's tokens by kind from its transcript, replacing its last report",
		Long: "Reads every model message the session's transcript and its subagents' hold,\n" +
			"counted once each, and sends their tokens by kind. A message's lines repeat its\n" +
			"usage, so each counts once by its id, at the figure with the most output.\n" +
			"\n" +
			"Run as a Claude Code hook, it reads the session_id and transcript_path every\n" +
			"hook is handed on stdin. Run by hand, --transcript names the file, and the\n" +
			"session is the file's name. Reporting the same messages again changes nothing\n" +
			"and warns.",
		Example: "  slate sessions report --transcript ~/.claude/projects/-home-me-src-app/<session>.jsonl\n" +
			"  # A SessionEnd or Stop hook in ~/.claude/settings.json:\n" +
			"  #   {\"type\": \"command\", \"command\": \"slate sessions report >/dev/null\", \"timeout\": 30}",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			session, transcriptPath, err := sessionToReport(cmd.InOrStdin(), path)
			if err != nil {
				return err
			}
			messages, err := transcript.SessionMessages(transcriptPath)
			if err != nil {
				return fmt.Errorf("read the transcript of session %s: %w", session, err)
			}
			report := api.SessionUsageReport{Messages: make([]api.MessageTokens, 0, len(messages))}
			if host, err := os.Hostname(); err == nil {
				report.Host = nonEmpty(host)
			}
			for _, m := range messages {
				report.Messages = append(report.Messages, api.MessageTokens{
					Key:       m.Key,
					WrittenTS: m.WrittenAt,
					Model:     nonEmpty(m.Model),
					Subagent:  nonEmpty(m.Subagent),
					Tokens:    api.Tokens(m.Tokens),
				})
			}
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.ReportSessionUsage(cmd.Context(), session, report, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			summary := fmt.Sprintf("Session %s spent %s over %d messages", session, reportedTokens(written.Session.Tokens), written.Session.MessageCount)
			if slices.ContainsFunc(written.Warnings, func(w api.WriteEffect) bool { return slices.Contains(usageNotReplaced, w.Code) }) {
				summary = ""
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, summary)
		},
	}
	cmd.Flags().StringVar(&path, "transcript", "", "The session's transcript, <session>.jsonl (default: the hook event on stdin)")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

// sessionToReport is the session and its transcript: the file --transcript
// names, else the hook event on stdin, which a terminal never carries.
func sessionToReport(stdin io.Reader, path string) (session, transcriptPath string, err error) {
	if path != "" {
		return strings.TrimSuffix(filepath.Base(path), ".jsonl"), path, nil
	}
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return "", "", goclikit.UsageError(errors.New("pass --transcript, or run it as a Claude Code hook, which hands it the session on stdin"))
	}
	var event hookEvent
	if err := json.NewDecoder(stdin).Decode(&event); err != nil {
		return "", "", goclikit.UsageError(fmt.Errorf("read the hook event on stdin: %w", err))
	}
	if event.SessionID == "" || event.TranscriptPath == "" {
		return "", "", goclikit.UsageError(errors.New("the hook event on stdin names no session_id and transcript_path"))
	}
	return event.SessionID, event.TranscriptPath, nil
}

func newSessionsShowCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show <session>",
		Short: "Show a session's tokens by kind: in all, in each worklog, and outside them",
		Long: "The tokens are as the session last reported them. A session that has not\n" +
			"reported shows none.",
		Example: "  slate sessions show \"$" + claudeCodeSessionEnv + "\"\n" +
			"  slate sessions show 6b3f0c1e-2d4a-4e8b-9c7d-0a1b2c3d4e51 --json",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			session, err := client.GetSession(cmd.Context(), args[0])
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), session)
			}
			printSessionDetail(cmd.OutOrStdout(), session, time.Now())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the session as JSON to stdout")
	return cmd
}

func printSessionDetail(out io.Writer, session api.AgentSession, now time.Time) {
	p := gotermstyle.PaletteFor(out)
	_, _ = fmt.Fprintln(out, p.Cyan(session.ID))
	_, _ = fmt.Fprintf(out, "  host:      %s\n", orDash(strValue(session.Host)))
	_, _ = fmt.Fprintf(out, "  seen:      %s\n", localClock(session.FirstSeenTS, now))
	if session.UsageReportedTS == nil {
		_, _ = fmt.Fprintln(out, "  reported:  never")
		return
	}
	_, _ = fmt.Fprintf(out, "  reported:  %s, %d messages\n", localClock(*session.UsageReportedTS, now), session.MessageCount)
	_, _ = fmt.Fprintf(out, "  tokens:    %s\n", reportedTokens(session.Tokens))
	_, _ = fmt.Fprintf(out, "  outside:   %s\n", reportedTokens(session.TokensOutsideWorklogs))
	if len(session.Spans) > 0 {
		_, _ = fmt.Fprintf(out, "\nWorklogs (%d):\n", len(session.Spans))
		for _, span := range session.Spans {
			_, _ = fmt.Fprintf(out, "  %s  %s\n    %s\n", span.Worklog, spanTimes(span.StartedTS, span.EndedTS, now, "still in it"), reportedTokens(span.Tokens))
		}
	}
}

// reportedTokens is tokensLine for a sum the API leaves null while no
// message it covers was reported.
func reportedTokens(t *api.Tokens) string {
	if t == nil {
		return "none reported"
	}
	return tokensLine(*t)
}

// tokensLine names each kind's count, never a total across them.
func tokensLine(t api.Tokens) string {
	return fmt.Sprintf("%s uncached input · %s cache write · %s cache read · %s output",
		groupedDigits(t.UncachedInput), groupedDigits(t.CacheWrite), groupedDigits(t.CacheRead), groupedDigits(t.Output))
}

// groupedDigits writes n with a comma between each group of three digits.
func groupedDigits(n int) string {
	digits := strconv.Itoa(n)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	return sign + b.String()
}
