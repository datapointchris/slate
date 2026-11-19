package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

var checkHints = []string{
	"List a record's checks: slate checks list <number>",
	"List what is due a check: slate checks due",
}

func newChecksCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "checks",
		Short: "Read and add checks on issues and initiatives, and see which come due next",
		Long: "A check reads an issue or an initiative against the world it describes and\n" +
			"names what it found as a verdict, which `slate vocabulary` lists, with the\n" +
			"evidence and what it cites. Checks are never edited, so a record's checks are\n" +
			"the history of whether it still held. `slate show` and `slate next --json`\n" +
			"carry each record's latest.\n" +
			"\n" +
			"`due` is the order records come due: incoming issues, records marked with\n" +
			"`request`, records never checked, the queue's head checked too long ago, then\n" +
			"everything else checked longer ago still. A record under a live claim is never due.",
		Example: "  slate checks due -n 20 --json                                 the next batch to read\n" +
			"  slate checks add 412 --verdict holds --evidence \"Still fails\"  record what a read found\n" +
			"  slate checks request 412 --reason \"Its paths moved\"           ask for a check next\n" +
			"  slate checks list 412                                         read its checks, latest first",
	})
	goclikit.WithRecoveryHints(cmd, checkHints...)
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupReading,
		goclikit.WithRecoveryHints(newChecksListCommand(), checkHints...),
		goclikit.WithRecoveryHints(newChecksDueCommand(), checkHints...),
	)...)
	cmd.AddCommand(inGroup(groupChanging,
		goclikit.WithRecoveryHints(newChecksAddCommand(), checkHints...),
		goclikit.WithRecoveryHints(newChecksRequestCommand(), checkHints...),
		goclikit.WithRecoveryHints(newChecksWithdrawCommand(), checkHints...),
	)...)
	return cmd
}

func newChecksListCommand() *cobra.Command {
	var (
		asJSON bool
		limit  int
	)
	listed := fetchedType((*api.Client).ListRecordChecks)
	cmd := &cobra.Command{
		Use:   "list <number>",
		Short: "List an issue's or an initiative's checks, latest first",
		Long: "--json prints a list of checks, each with the keys\n" +
			"  " + jsonKeys(listed) + "\n" +
			"cites holds each source as {kind, ref}.",
		Example:     "  slate checks list 412\n  slate checks list 412 -n 1 --json",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			checks, err := client.ListRecordChecks(cmd.Context(), args[0], limitFlag(cmd))
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), checks)
			}
			if len(checks) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No checks.")
				return nil
			}
			printChecks(cmd.OutOrStdout(), checks, time.Now())
			return nil
		},
	}
	addLimitFlag(cmd, &limit)
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output checks as JSON to stdout")
	return cmd
}

func newChecksDueCommand() *cobra.Command {
	var (
		asJSON bool
		limit  int
	)
	listed := fetchedType((*api.Client).DueChecks)
	cmd := &cobra.Command{
		Use:   "due",
		Short: "List the open issues and initiatives due a check, first due first",
		Long: "Each row says why it is due: incoming, requested, never_checked, queue_head\n" +
			"or stale. Under each row are the open records whose titles read like it.\n" +
			"\n" +
			"--json prints a list, each with the keys\n" +
			"  " + jsonKeys(listed),
		Example:     "  slate checks due\n  slate checks due -n 20 --json",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			due, err := client.DueChecks(cmd.Context(), limitFlag(cmd))
			if err != nil {
				return handleAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), due)
			}
			if len(due) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Nothing is due a check.")
				return nil
			}
			printDueChecks(cmd.OutOrStdout(), due, time.Now())
			return nil
		},
	}
	addLimitFlag(cmd, &limit)
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the due records as JSON to stdout")
	return cmd
}

func newChecksAddCommand() *cobra.Command {
	var (
		verdict  string
		evidence string
		cites    []string
		run      string
		session  string
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:   "add <number> --verdict <verdict> --evidence <text> [--cite <kind>:<ref>]...",
		Short: "Record what reading an issue or an initiative against the world found",
		Long: "--verdict is one of `slate vocabulary`'s check verdicts. --evidence is what\n" +
			"the read found, in prose a person reads cold. Each --cite is a source, as its\n" +
			"kind, a colon, and the reference: issue:412, path:slate:cli/main.go:12,\n" +
			"commit:slate@4e9aec7, url:https://example.com. The check clears the record's\n" +
			"check request, and is signed as the login slate runs under.",
		Example: "  slate checks add 412 --verdict holds --evidence \"The export still gives up on a timeout\"\n" +
			"  slate checks add 412 --verdict done --evidence \"Shipped in 4e9aec7\" --cite commit:slate@4e9aec7",
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(verdict) == "" || strings.TrimSpace(evidence) == "" {
				return goclikit.UsageError(errors.New("a check needs --verdict and --evidence"))
			}
			in := api.CheckAddInput{Verdict: verdict, Evidence: evidence, Cites: []api.CheckCite{}}
			for _, cite := range cites {
				kind, ref, found := strings.Cut(cite, ":")
				if !found || strings.TrimSpace(kind) == "" || strings.TrimSpace(ref) == "" {
					return goclikit.UsageError(fmt.Errorf("--cite %q: give its kind, a colon and the reference, as issue:412", cite))
				}
				in.Cites = append(in.Cites, api.CheckCite{Kind: kind, Ref: ref})
			}
			if run != "" {
				in.Run = &run
			}
			place := agentPlace(session)
			in.Session, in.Host = place.Session, place.Host
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.AddRecordCheck(cmd.Context(), args[0], in)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			summary := fmt.Sprintf("Checked #%d: %s", written.Check.RecordNumber, written.Check.Verdict)
			return reportWrite(cmd, asJSON, written, written.WriteEffects, summary)
		},
	}
	f := cmd.Flags()
	f.StringVar(&verdict, "verdict", "", "What the read found, one of `slate vocabulary`'s check verdicts")
	f.StringVar(&evidence, "evidence", "", "What shows it, in prose")
	f.StringArrayVar(&cites, "cite", nil, "A source the evidence rests on, as <kind>:<ref> (repeatable)")
	f.StringVar(&run, "run", "", "The scheduled run that made the check, where one did")
	addActingSessionFlag(f, &session, agentSessionHelp+", which made the check")
	f.BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newChecksRequestCommand() *cobra.Command {
	var (
		reason string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "request <number> --reason <text>",
		Short: "Mark an issue or an initiative to be checked next, ahead of its interval",
		Long: "The mark puts the record in `slate checks due` after the incoming issues,\n" +
			"until a check lands on it or `withdraw` clears it. A record already marked\n" +
			"keeps the mark it has.",
		Example:     "  slate checks request 412 --reason \"The exporter it names was rewritten\"",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeChecksRequest),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(reason) == "" {
				return goclikit.UsageError(errors.New("a check request needs --reason, saying what to check"))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.RequestCheck(cmd.Context(), args[0], reason)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, fmt.Sprintf("Marked #%d for a check", written.Number))
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "What to check, and why now")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newChecksWithdrawCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:         "withdraw <number>",
		Short:       "Clear an issue's or an initiative's check request, leaving it due at its interval",
		Example:     "  slate checks withdraw 412",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeChecksRequest),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.WithdrawCheckRequest(cmd.Context(), args[0])
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, fmt.Sprintf("Withdrew the check request on #%d", written.Number))
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func printChecks(out io.Writer, checks []api.RecordCheck, now time.Time) {
	for i, check := range checks {
		if i > 0 {
			_, _ = fmt.Fprintln(out)
		}
		line := fmt.Sprintf("  %s  %s  %s", localClock(check.CheckedTS, now), check.Verdict, check.CheckedBy)
		if run := strValue(check.Run); run != "" {
			line += "  run " + run
		}
		_, _ = fmt.Fprintln(out, line)
		_, _ = fmt.Fprintln(out, indent(check.Evidence, "      "))
		if len(check.Cites) > 0 {
			cited := make([]string, 0, len(check.Cites))
			for _, cite := range check.Cites {
				cited = append(cited, cite.Kind+":"+cite.Ref)
			}
			_, _ = fmt.Fprintf(out, "      cites: %s\n", strings.Join(cited, ", "))
		}
	}
}

func printDueChecks(out io.Writer, due []api.DueCheck, now time.Time) {
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NUMBER\tKIND\tDUE\tPRIORITY\tLAST CHECK\tTITLE")
	for _, row := range due {
		_, _ = fmt.Fprintf(tw, "#%d\t%s\t%s\t%s\t%s\t%s\n",
			row.Number, row.Kind, row.DueBecause, priorityName(row.Priority), lastCheckCell(row.LastCheck, now), row.Title)
		for _, match := range row.LikelyDuplicates {
			_, _ = fmt.Fprintf(tw, "\t\t\t\t\t  reads like %s\n", recordSummaryLine(match))
		}
	}
	_ = tw.Flush()
}

func lastCheckCell(last *api.LastCheck, now time.Time) string {
	if last == nil {
		return "-"
	}
	return last.Verdict + " " + localClock(last.CheckedTS, now)
}

// lastCheckNote is a detail view's line for a record's latest check, or empty
// where it has none.
func lastCheckNote(last *api.LastCheck) string {
	if last == nil {
		return ""
	}
	return fmt.Sprintf("%s by %s on %s", last.Verdict, last.CheckedBy, localDay(last.CheckedTS))
}

// checkRequestNote is a detail view's line for a record's check request, or
// empty where it has none.
func checkRequestNote(request *api.CheckRequest) string {
	if request == nil {
		return ""
	}
	return fmt.Sprintf("%s — by %s on %s", request.Reason, request.RequestedBy, localDay(request.RequestedTS))
}
