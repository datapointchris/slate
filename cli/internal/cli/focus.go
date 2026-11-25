package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/datapointchris/goclikit"
	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

const focusSetUsage = "slate focus set --statement <text> --until <YYYY-MM-DD> [--share <pursuit>=<fraction>]..."

// A focus command meets a 404 where a share names no pursuit, or where the API
// predates the focus routes.
var focusHints = []string{
	"List the active pursuits a share can name: slate pursuits list",
	"See which API slate is pointed at: slate auth status",
}

func newFocusCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "focus",
		Short: "State where the work should go for a while, as a share per pursuit, and read each pursuit's standing",
		Long: "A focus is a statement of where the work should go, the last day it holds, and\n" +
			"the share of the work each pursuit should get. One is in force at a time, and\n" +
			"setting one ends the one before it. Every focus stays, so `slate focus list`\n" +
			"reads them all.\n" +
			"\n" +
			"`slate focus show` reads each pursuit's standing against its share: its usage,\n" +
			"the part of the window's output tokens it drew, and its factor, 2^(-usage/share).\n" +
			"A factor reads 0.5 at its share, toward 1 under it and toward 0 over it. A\n" +
			"reader ordering the queue takes slate's factor rather than working it out.",
		Example: "  slate focus show\n" +
			"  slate focus usage\n" +
			"  slate focus set --statement \"Warehouse first this month\" --until 2026-12-31 --share \"Data Warehouse=0.6\"\n" +
			"  slate focus end --reason \"The audit finished early\"\n" +
			"  slate focus list",
	})
	goclikit.WithRecoveryHints(cmd, focusHints...)
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupReading,
		newFocusShowCommand(),
		newFocusListCommand(),
		newFocusUsageCommand(),
	)...)
	cmd.AddCommand(inGroup(groupChanging,
		newFocusSetCommand(),
		newFocusEndCommand(),
	)...)
	return cmd
}

func newFocusShowCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the focus in force and each pursuit's share, usage, factor and last work",
		Long: "Usage is a pursuit's part of the output tokens every pursuit drew in the window,\n" +
			"which opens at the later of the focus's start and 30 days ago. A worklog's output\n" +
			"splits evenly across the pursuits its record reaches, an issue through its\n" +
			"initiative. A share marked (rest) is the focus leaving that pursuit unnamed, so it\n" +
			"takes an even part of what the named shares left. With no focus in force, SHARE\n" +
			"and FACTOR read -, and usage still reads.\n" +
			"\n" +
			"UNATTRIBUTED is the output of worklogs whose record reaches no pursuit, and the\n" +
			"output sessions spent in no worklog.",
		Example:     "  slate focus show\n  slate focus show --json",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			standing, err := client.GetFocusStanding(cmd.Context(), issueZone())
			if err != nil {
				return handleAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), standing)
			}
			printFocusStanding(cmd.OutOrStdout(), standing, time.Now())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the focus and each pursuit's standing as JSON to stdout")
	return cmd
}

func newFocusListCommand() *cobra.Command {
	var (
		asJSON bool
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every focus, the latest set first",
		Long: "ENDED reads - for the focus in force, lapsed for one whose last day passed with\n" +
			"nothing ending it, and otherwise when it ended and why. A set ends the focus\n" +
			"before it as replaced, or as lapsed where its last day had passed.",
		Example:     "  slate focus list\n  slate focus list --limit 3 --json",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			focuses, err := client.ListFocuses(cmd.Context(), issueZone(), limitFlag(cmd))
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), focuses)
			}
			printFocuses(cmd.OutOrStdout(), focuses, time.Now())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output every focus as JSON to stdout")
	addLimitFlag(cmd, &limit)
	return cmd
}

// errFocusUsageUnanswered is an API older than the usage by day, which answers its route as no
// route at all.
var errFocusUsageUnanswered = errors.New("this API answers no usage by day, so it is older than this CLI")

// focusUsageRead is GetFocusUsage, naming an API older than the route by errFocusUsageUnanswered.
func focusUsageRead(ctx context.Context, client *api.Client, id *int) (api.FocusUsage, error) {
	usage, err := client.GetFocusUsage(ctx, id, issueZone())
	if noRouteFor(err) {
		return api.FocusUsage{}, errFocusUsageUnanswered
	}
	return usage, err
}

func newFocusUsageCommand() *cobra.Command {
	var (
		asJSON  bool
		focusID int
	)
	cmd := &cobra.Command{
		Use:   "usage [--focus <id>]",
		Short: "Show each pursuit's usage on each day of a focus, beside its share",
		Long: "Reads the focus in force, or the one --focus names by its id from\n" +
			"`slate focus list`, a day at a time. Each day's usage is over the window ending\n" +
			"at that day's midnight, as `slate focus show` counts it now, with the pursuits\n" +
			"active now. So only the last day of the focus in force equals `slate focus show`.\n" +
			"A focus longer than 30 days shows its last 30. With no focus in force, it shows\n" +
			"the last 30 days, and SHARE and FACTOR read ·.\n" +
			"\n" +
			"The rows come in the last day's order, the furthest under its share first, then\n" +
			"any pursuit that only an earlier day lists. SHARE, USAGE and FACTOR are the last\n" +
			"day's. BY DAY is a glyph per day, each pursuit's usage scaled to its own highest,\n" +
			"and blank on a day it drew nothing.",
		Example:     "  slate focus usage\n  slate focus usage --focus 3\n  slate focus usage --json | jq '.days[-1].pursuits[] | {number, usage, factor}'",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var id *int
			if cmd.Flags().Changed("focus") {
				id = &focusID
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			usage, err := focusUsageRead(cmd.Context(), client, id)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), usage)
			}
			printFocusUsage(cmd.OutOrStdout(), usage, time.Now())
			return nil
		},
	}
	cmd.Flags().IntVar(&focusID, "focus", 0, "The focus to read, by its id (see: slate focus list); the one in force where none is named")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the focus and each day's figures as JSON to stdout")
	return cmd
}

func newFocusSetCommand() *cobra.Command {
	var (
		statement, until string
		shares           []string
		asJSON           bool
	)
	cmd := &cobra.Command{
		Use:   "set --statement <text> --until <YYYY-MM-DD> [--share <pursuit>=<fraction>]...",
		Short: "Set a focus from now, ending the one in force",
		Long: "--statement says where the work should go, in your words. --until is the last\n" +
			"day the focus holds. --share names a pursuit by number or exact name, and the\n" +
			"fraction of the work it should get, from 0 to 1. It repeats, once per pursuit.\n" +
			"\n" +
			"The shares sum to 1 at most, and each names an active pursuit. The active\n" +
			"pursuits no share names split the rest of 1 evenly. A set naming what the focus\n" +
			"in force already holds changes nothing.",
		Example: "  slate focus set --statement \"Warehouse first this month\" --until 2026-12-31 --share \"Data Warehouse=0.6\"\n" +
			"  slate focus set --statement \"Even split\" --until 2026-12-31\n" +
			"  slate focus set --statement \"Warehouse and queue\" --until 2026-12-15 --share 2950=0.5 --share 3010=0.3",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeFocusWrite, scopeRecordsRead),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(statement) == "" {
				return goclikit.UsageError(errors.New("--statement is required — say where the work should go"))
			}
			if until == "" {
				return goclikit.UsageError(errors.New("--until is required — the last day the focus holds, YYYY-MM-DD"))
			}
			if err := parseDay("until", until); err != nil {
				return err
			}
			named, err := parseShares(shares)
			if err != nil {
				return err
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			in := api.FocusSetInput{Statement: statement, UntilDate: until, Shares: make([]api.FocusShareSet, 0, len(named))}
			refs := make([]string, 0, len(named))
			for _, share := range named {
				refs = append(refs, share.pursuit)
			}
			numbers, err := pursuitNumbers(cmd, client, refs)
			if err != nil {
				return err
			}
			for i, share := range named {
				in.Shares = append(in.Shares, api.FocusShareSet{Pursuit: numbers[i], Share: share.fraction})
			}
			written, err := client.SetFocus(cmd.Context(), in, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, focusSetSummary(written))
		},
	}
	cmd.Flags().StringVar(&statement, "statement", "", "Where the work should go, in your words (required)")
	cmd.Flags().StringVar(&until, "until", "", "The last day the focus holds, YYYY-MM-DD (required)")
	cmd.Flags().StringArrayVar(&shares, "share", nil, "A pursuit by number or exact name, and its fraction of the work: <pursuit>=<fraction> (repeatable; see: slate pursuits list)")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newFocusEndCommand() *cobra.Command {
	var (
		reason string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:         "end --reason <why>",
		Short:       "End the focus in force",
		Long:        "--reason is required, so whoever reads the history knows why it ended early.",
		Example:     "  slate focus end --reason \"The audit finished early\"",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeFocusWrite),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(reason) == "" {
				return goclikit.UsageError(errors.New("--reason is required — say why the focus ends"))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.EndFocus(cmd.Context(), reason, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			summary := ""
			if written.Focus != nil {
				summary = fmt.Sprintf("Ended focus %d, %q: %s", written.Focus.ID, written.Focus.Statement, reason)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, summary)
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Why the focus ends (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

// namedShare is one --share as typed: the pursuit's number or name, and its fraction.
type namedShare struct {
	pursuit  string
	fraction float64
}

// parseShares reads each --share as <pursuit>=<fraction>, refusing a malformed
// one before any request. It splits at the last =, so a name holding one still
// reads.
func parseShares(values []string) ([]namedShare, error) {
	shares := make([]namedShare, 0, len(values))
	for _, value := range values {
		i := strings.LastIndex(value, "=")
		if i < 0 {
			return nil, goclikit.UsageError(fmt.Errorf("--share %q: a share is <pursuit>=<fraction>, such as \"Data Warehouse=0.6\"", value))
		}
		pursuit, text := strings.TrimSpace(value[:i]), strings.TrimSpace(value[i+1:])
		if pursuit == "" {
			return nil, goclikit.UsageError(fmt.Errorf("--share %q names no pursuit before the =", value))
		}
		fraction, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsNaN(fraction) || fraction < 0 || fraction > 1 {
			return nil, goclikit.UsageError(fmt.Errorf("--share %q: the share is a fraction from 0 to 1, such as 0.6", value))
		}
		shares = append(shares, namedShare{pursuit: pursuit, fraction: fraction})
	}
	return shares, nil
}

// focusSetSummary names the focus set and its shares, or nothing where the set
// changed nothing, whose warning says so.
func focusSetSummary(written api.FocusWritten) string {
	if written.Focus == nil {
		return ""
	}
	for _, warning := range written.Warnings {
		if warning.Code == "focus_unchanged" {
			return ""
		}
	}
	focus := written.Focus
	line := fmt.Sprintf("Set focus %d through %s, %q", focus.ID, focus.UntilDate, focus.Statement)
	if len(focus.Shares) == 0 {
		return line + ", naming no share, so the active pursuits split the work evenly"
	}
	return line + ", with shares " + focusSharesLine(focus.Shares)
}

// focusSharesLine is each named share: its pursuit's number and name, and its fraction.
func focusSharesLine(shares []api.FocusShare) string {
	shown := make([]string, 0, len(shares))
	for _, share := range shares {
		shown = append(shown, fmt.Sprintf("#%d %s=%s", share.Pursuit.Number, share.Pursuit.Name, fraction(share.Share)))
	}
	return strings.Join(shown, ", ")
}

func fraction(f float64) string {
	return strconv.FormatFloat(f, 'f', 2, 64)
}

func fractionOrDash(f *float64) string {
	if f == nil {
		return "-"
	}
	return fraction(*f)
}

func printFocusStanding(out io.Writer, standing api.FocusStanding, now time.Time) {
	if focus := standing.Focus; focus != nil {
		_, _ = fmt.Fprintf(out, "Focus %d, set %s by %s, holds through %s\n", focus.ID, localClock(focus.StartedTS, now), focus.SetBy, focus.UntilDate)
		_, _ = fmt.Fprintf(out, "  %s\n\n", focus.Statement)
	} else {
		_, _ = fmt.Fprintf(out, "No focus is in force. Set one with `%s`.\n\n", focusSetUsage)
	}
	_, _ = fmt.Fprintf(out, "Usage counts output tokens from %s to %s.\n\n",
		localClock(standing.Window.Since, now), localClock(standing.Window.Until, now))
	if len(standing.Pursuits) == 0 {
		_, _ = fmt.Fprintln(out, "No active pursuits, and none drew output in the window.")
	} else {
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "PURSUIT\tSTATUS\tSHARE\tUSAGE\tFACTOR\tOUTPUT\tWORKLOGS\tLAST WORKED\tNAME")
		for _, pursuit := range standing.Pursuits {
			share := fractionOrDash(pursuit.Share)
			if pursuit.Share != nil && !pursuit.ShareNamed {
				share += " (rest)"
			}
			touched := "-"
			if pursuit.TouchedTS != nil {
				touched = localClock(*pursuit.TouchedTS, now)
			}
			_, _ = fmt.Fprintf(tw, "#%d\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
				pursuit.Number, pursuit.Status, share, fraction(pursuit.Usage), fractionOrDash(pursuit.Factor),
				groupedDigits(int(math.Round(pursuit.SplitOutputTokens))), pursuit.WorklogCount, touched, pursuit.Name)
		}
		_ = tw.Flush()
	}
	unattributed := standing.Unattributed
	_, _ = fmt.Fprintf(out, "\nUNATTRIBUTED  %s output in %d %s reaching no pursuit · %s output in no worklog\n",
		groupedDigits(unattributed.OutputTokens), unattributed.WorklogCount, plural(unattributed.WorklogCount, "worklog", "worklogs"),
		groupedDigits(unattributed.OutsideWorklogsOutputTokens))
}

// noFigure stands in a cell for a share or factor that the read holds none of, and for a usage
// or output of zero.
const noFigure = "·"

func figureOrDot(f *float64) string {
	if f == nil {
		return noFigure
	}
	return fraction(*f)
}

// pursuitDays is one pursuit's figures on each day of a usage read, nil on a day that lacks it.
type pursuitDays struct {
	pursuit api.FocusUsagePursuit
	days    []*api.PursuitUsageOnDay
}

// last is the pursuit's figures on the read's last day, nil where that day lacks it.
func (p pursuitDays) last() *api.PursuitUsageOnDay {
	if len(p.days) == 0 {
		return nil
	}
	return p.days[len(p.days)-1]
}

// byPursuit is each pursuit the read lists, with its figures on each day: in the last day's
// order, the furthest under its share first, then each that only an earlier day lists, in number
// order.
func byPursuit(usage api.FocusUsage) []pursuitDays {
	at := map[int]int{}
	var rows []pursuitDays
	add := func(number int) int {
		if i, seen := at[number]; seen {
			return i
		}
		pursuit := api.FocusUsagePursuit{PursuitSummary: api.PursuitSummary{Number: number}}
		if found := slices.IndexFunc(usage.Pursuits, func(p api.FocusUsagePursuit) bool { return p.Number == number }); found >= 0 {
			pursuit = usage.Pursuits[found]
		}
		at[number] = len(rows)
		rows = append(rows, pursuitDays{pursuit: pursuit, days: make([]*api.PursuitUsageOnDay, len(usage.Days))})
		return at[number]
	}
	if n := len(usage.Days); n > 0 {
		for _, figures := range usage.Days[n-1].Pursuits {
			add(figures.Number)
		}
	}
	for _, pursuit := range usage.Pursuits {
		add(pursuit.Number)
	}
	for day := range usage.Days {
		for i := range usage.Days[day].Pursuits {
			figures := &usage.Days[day].Pursuits[i]
			rows[add(figures.Number)].days[day] = figures
		}
	}
	return rows
}

// usageSpark is a glyph per day, scaled to the pursuit's highest usage, and blank on a day it
// drew nothing or that lacks it.
func usageSpark(days []*api.PursuitUsageOnDay) string {
	glyphs := []rune(sparkGlyphs)
	most := 0.0
	for _, day := range days {
		if day != nil {
			most = max(most, day.Usage)
		}
	}
	var b strings.Builder
	for _, day := range days {
		if day == nil || day.Usage <= 0 || most <= 0 {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(glyphs[min(len(glyphs)-1, int(day.Usage/most*float64(len(glyphs)-1)+0.5))])
	}
	return b.String()
}

// focusDays is how many calendar days a focus spans, from the day it started to lastDay.
func focusDays(focus api.Focus, lastDay string) int {
	first, err := time.ParseInLocation(time.DateOnly, focus.StartedTS.Local().Format(time.DateOnly), time.Local)
	if err != nil {
		return 0
	}
	last, err := time.ParseInLocation(time.DateOnly, lastDay, time.Local)
	if err != nil {
		return 0
	}
	return int(last.Sub(first).Round(24*time.Hour).Hours()/24) + 1
}

// usageCut says the read answers only the last days of a focus longer than it reads, as "the
// last 30 of its 45 days", and nothing where it answers every day.
func usageCut(usage api.FocusUsage) string {
	if usage.Focus == nil || len(usage.Days) == 0 || !usage.Since.After(usage.Focus.StartedTS) {
		return ""
	}
	return fmt.Sprintf("the last %d of its %d days", len(usage.Days), focusDays(*usage.Focus, usage.Days[len(usage.Days)-1].Day))
}

func printFocusUsage(out io.Writer, usage api.FocusUsage, now time.Time) {
	if focus := usage.Focus; focus != nil {
		_, _ = fmt.Fprintf(out, "Focus %d, set %s by %s, ", focus.ID, localClock(focus.StartedTS, now), focus.SetBy)
		if focus.Active {
			_, _ = fmt.Fprintf(out, "holds through %s\n", focus.UntilDate)
		} else {
			_, _ = fmt.Fprintf(out, "held through %s, ended %s\n", focus.UntilDate, focusEnded(*focus, now))
		}
		_, _ = fmt.Fprintf(out, "  %s\n\n", focus.Statement)
	} else {
		_, _ = fmt.Fprintln(out, "No focus is in force, so these are the last 30 days, with no share to stand against.")
		_, _ = fmt.Fprintln(out)
	}
	if len(usage.Days) == 0 {
		_, _ = fmt.Fprintln(out, "No day to show.")
		return
	}
	days := fmt.Sprintf("Days %s to %s", usage.Days[0].Day, usage.Days[len(usage.Days)-1].Day)
	if cut := usageCut(usage); cut != "" {
		days += ", " + cut
	}
	_, _ = fmt.Fprintf(out, "%s, counted from %s to %s.\n\n", days, localClock(usage.Since, now), localClock(usage.Until, now))
	rows := byPursuit(usage)
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(out, "No active pursuits, and none drew output on these days.")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "PURSUIT\tSHARE\tUSAGE\tFACTOR\tBY DAY\tNAME")
	for _, row := range rows {
		share, used, factor := noFigure, noFigure, noFigure
		if last := row.last(); last != nil {
			share, factor = figureOrDot(last.Share), figureOrDot(last.Factor)
			if last.Share != nil && !last.ShareNamed {
				share += " (rest)"
			}
			if last.Usage > 0 {
				used = fraction(last.Usage)
			}
		}
		_, _ = fmt.Fprintf(tw, "#%d\t%s\t%s\t%s\t%s\t%s\n", row.pursuit.Number, share, used, factor, usageSpark(row.days), row.pursuit.Name)
	}
	_ = tw.Flush()
}

// focusEnded says how a focus stands: - in force, lapsed where its last day
// passed with nothing ending it, else when it ended and why.
func focusEnded(focus api.Focus, now time.Time) string {
	switch {
	case focus.EndedTS != nil:
		return localClock(*focus.EndedTS, now) + " " + strValue(focus.EndedReason)
	case focus.Active:
		return "-"
	}
	return "lapsed"
}

func printFocuses(out io.Writer, focuses []api.Focus, now time.Time) {
	if len(focuses) == 0 {
		_, _ = fmt.Fprintf(out, "No focus has been set. Set one with `%s`.\n", focusSetUsage)
		return
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "FOCUS\tSET\tBY\tUNTIL\tENDED\tSHARES\tSTATEMENT")
	for _, focus := range focuses {
		shares := "-"
		if len(focus.Shares) > 0 {
			shares = focusSharesLine(focus.Shares)
		}
		_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			focus.ID, localClock(focus.StartedTS, now), focus.SetBy, focus.UntilDate, focusEnded(focus, now), shares, focus.Statement)
	}
	_ = tw.Flush()
}
