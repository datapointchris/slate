package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/gotermstyle"
	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

func newTotalsCommand() *cobra.Command {
	var (
		asJSON bool
		repo   string
	)
	cmd := &cobra.Command{
		Use:        "totals [<number> | <worklog>]",
		SuggestFor: []string{"summary", "usage", "stats"},
		Short:      "Total the sessions, commits, pull requests, landings and tokens of a record, worklog, repo or window",
		Long: "Prints how many worklogs, sessions, commits, pull requests, landings and\n" +
			"messages a scope holds, and its tokens by kind, in all and by model.\n" +
			"\n" +
			"A number totals that record and everything under it. An issue reaches the\n" +
			"issues under it. An initiative reaches the idea it was promoted from, every\n" +
			"issue in it and every issue under those. A worklog name, such as 412.2,\n" +
			"totals that worklog. --repo totals the worklogs of that repo's issues. Named\n" +
			"together, they narrow together.\n" +
			"\n" +
			"With none of them, the totals reach every worklog and every token spent\n" +
			"outside one. --since and --until narrow every figure to what happened inside\n" +
			"the window, so a window's edge splits a session's tokens by when each message\n" +
			"was written.",
		Example: "  slate totals 412\n" +
			"  slate totals --since 7d\n" +
			"  slate totals --repo slate --since 2026-10-26 --until 2026-11-02\n" +
			"  slate totals 412.2 --json",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var filter api.TotalsFilter
			if len(args) == 1 {
				if name := strings.TrimPrefix(args[0], "#"); isWorklogName(name) {
					filter.Worklog = name
				} else {
					filter.Record = args[0]
				}
			}
			var err error
			if filter.Repo, err = repoFlagValue(cmd, repo); err != nil {
				return err
			}
			window, err := readWindow(cmd)
			if err != nil {
				return err
			}
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			totals, err := client.GetTotals(cmd.Context(), filter, window)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), totals)
			}
			printTotals(cmd.OutOrStdout(), totals, time.Now())
			return nil
		},
	}
	addWindowFlags(cmd, "work done")
	cmd.Flags().StringVar(&repo, "repo", "", "Only the worklogs of this repo's issues; empty for the issues on no repo")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the totals as JSON to stdout")
	return goclikit.WithRecoveryHints(cmd, searchHint, worklogHints[0])
}

func printTotals(out io.Writer, totals api.WorkTotals, now time.Time) {
	scope := totals.Scope
	_, _ = fmt.Fprintln(out, totalsHeading(gotermstyle.PaletteFor(out), scope))
	_, _ = fmt.Fprintf(out, "  window:    %s\n", totalsWindow(scope, now))
	_, _ = fmt.Fprintf(out, "  worklogs:  %s\n", groupedDigits(totals.Worklogs))
	_, _ = fmt.Fprintf(out, "  sessions:  %s\n", groupedDigits(totals.Sessions))
	_, _ = fmt.Fprintf(out, "  links:     %s commits · %s pull requests · %s landings\n",
		groupedDigits(totals.Commits), groupedDigits(totals.PullRequests), groupedDigits(totals.Landings))
	_, _ = fmt.Fprintf(out, "  messages:  %s\n", groupedDigits(totals.MessageCount))
	_, _ = fmt.Fprintf(out, "  tokens:    %s\n", reportedTokens(totals.Tokens))
	if scope.Record == nil && scope.Worklog == nil && scope.Repo == nil {
		_, _ = fmt.Fprintf(out, "  outside:   %s\n", reportedTokens(totals.TokensOutsideWorklogs))
	}
	if len(totals.TokensByModel) > 0 {
		_, _ = fmt.Fprintf(out, "\nBy model (%d):\n", len(totals.TokensByModel))
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		for _, model := range totals.TokensByModel {
			_, _ = fmt.Fprintf(tw, "  %s\t%s messages\t%s\n",
				orDash(strValue(model.Model)), groupedDigits(model.MessageCount), tokensLine(model.Tokens))
		}
		_ = tw.Flush()
	}
}

// totalsHeading names what the totals reach: the record or worklog, then the
// repo, or every worklog where neither narrows them.
func totalsHeading(p gotermstyle.Palette, scope api.TotalsScope) string {
	var parts []string
	if record := scope.Record; record != nil {
		parts = append(parts, fmt.Sprintf("%s %s  %s", p.Cyan("#"+strconv.Itoa(record.Number)), p.Bold(record.Title), record.Kind))
	}
	if scope.Worklog != nil {
		parts = append(parts, "worklog "+p.Cyan(*scope.Worklog))
	}
	if scope.Repo != nil {
		if *scope.Repo == "" {
			parts = append(parts, "issues on no repo")
		} else {
			parts = append(parts, "repo "+*scope.Repo)
		}
	}
	if len(parts) == 0 {
		return p.Bold("All work")
	}
	return strings.Join(parts, " · ")
}

func totalsWindow(scope api.TotalsScope, now time.Time) string {
	switch {
	case scope.Since != nil && scope.Until != nil:
		return localClock(*scope.Since, now) + " to " + localClock(*scope.Until, now)
	case scope.Since != nil:
		return localClock(*scope.Since, now) + " to now"
	case scope.Until != nil:
		return "everything before " + localClock(*scope.Until, now)
	default:
		return "all time"
	}
}
