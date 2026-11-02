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
	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
	"github.com/datapointchris/slate/cli/internal/config"
)

// defaultClaimMinutes is a day, so a session paused overnight on a question
// still holds its record. A claim whose holder died is freed by `release`.
const defaultClaimMinutes = 1440

const maxClaimMinutes = 10080

var claimHints = []string{
	"List who holds what: slate claims list",
	"Search issues by title or description: slate issues search <query>",
}

func newClaimsCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "claims",
		Short: "Hold, extend, release and list the claims that say who is working a record",
		Long: "A claim is a lease on one record of any kind: an idea, an initiative or an\n" +
			"issue. It says who is working the record and until when. It holds no work:\n" +
			"the record keeps that. Every claim is kept after it ends, so a record's\n" +
			"claims are its history of who worked it.\n" +
			"\n" +
			"Another holder's live claim refuses a hold, and that is the only refusal.\n" +
			"Every other conflict a hold finds goes through with a warning. Nothing\n" +
			"extends a claim but `extend`. Every write that ends or removes a claim\n" +
			"names whose it was. A claim that runs out stops holding its record at once,\n" +
			"and ends as expired at its expiry when the next write finds it.",
	})
	goclikit.WithRecoveryHints(cmd, claimHints...)
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupReading, newClaimsListCommand())...)
	cmd.AddCommand(inGroup(groupChanging,
		goclikit.WithRecoveryHints(newClaimsHoldCommand(), claimHints...),
		goclikit.WithRecoveryHints(newClaimsExtendCommand(), claimHints...),
		goclikit.WithRecoveryHints(newClaimsReleaseCommand(), claimHints...),
	)...)
	return cmd
}

const claimActorHelp = "defaults to the config file's client_id when\n" +
	"SLATE_CLIENT_SECRET is set or token_command hands the token, since the API\n" +
	"takes no other name from a service. Otherwise it is who is running slate:\n" +
	"the Claude Code session, the worker $" + actorWorkerEnv + " names, or user@host."

func newClaimsHoldCommand() *cobra.Command {
	var (
		asJSON  bool
		next    bool
		queue   readyQueueFlags
		holder  string
		minutes int
	)
	cmd := &cobra.Command{
		Use:   "hold [<record>]",
		Short: "Hold a claim on a record, or on the next ready issue",
		Long: "Holds a claim on the record named, for --minutes. Another holder's live\n" +
			"claim refuses it, naming them. Everything else goes through, with a\n" +
			"warning on stderr naming what it found about the record and its neighbors,\n" +
			"such as an open dependency or a neighbor another holder claims. --json gives\n" +
			"each warning's code. Holding a record you already hold changes nothing: only\n" +
			"`extend` moves an expiry.\n" +
			"\n" +
			"A claim another holder let run out is ended as expired, and the answer says\n" +
			"so under the line naming the hold.\n" +
			"\n" +
			"--next holds the head of the issue ready queue in one request, so two\n" +
			"callers asking at once never hold the same issue. `slate issues next` under\n" +
			"the same flags shows what it would take. Decisions are left out unless --type\n" +
			"decision asks for them. An empty queue warns and exits 0.\n" +
			"\n" +
			"--holder " + claimActorHelp + " The claim also records the Claude Code session\n" +
			"from $CLAUDE_CODE_SESSION_ID, this host, and the working directory.",
		Example: "  slate claims hold 412\n" +
			"  slate claims hold 412 --minutes 60\n" +
			"  slate claims hold --next\n" +
			"  slate claims hold --next --repo webapp --json",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			if next == (len(args) == 1) {
				return goclikit.UsageError(errors.New("name a record or pass --next, not both"))
			}
			if !next {
				for _, name := range readyQueueFlagNames() {
					if f.Changed(name) {
						return goclikit.UsageError(fmt.Errorf("--%s narrows the queue --next takes from — a named record is not taken from it", name))
					}
				}
			}
			lease, err := claimLease(cmd, holder, minutes)
			if err != nil {
				return err
			}
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			var written api.ClaimWritten
			if next {
				filter, err := queue.filter(cmd)
				if err != nil {
					return err
				}
				written, err = client.HoldNextClaim(cmd.Context(), lease, filter, issueZone())
				if err != nil {
					return handleArgumentAPIError(err)
				}
			} else {
				written, err = client.HoldClaim(cmd.Context(), args[0], lease, issueZone())
				if err != nil {
					return handleArgumentAPIError(err)
				}
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, claimSummary("Holding", written.Claim, time.Now()))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&next, "next", false, "Hold the head of the issue ready queue instead of a named record")
	queue.register(cmd)
	f.StringVar(&holder, "holder", actorIdentity(), "Who holds the claim")
	addClaimMinutesFlag(cmd, &minutes)
	f.BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newClaimsExtendCommand() *cobra.Command {
	var (
		asJSON  bool
		holder  string
		minutes int
	)
	cmd := &cobra.Command{
		Use:   "extend <record>",
		Short: "Move your claim's expiry to --minutes from now",
		Long: "Refused unless --holder holds the record's claim. A claim of yours that\n" +
			"ran out is held again from now, with a warning that it had run out.\n" +
			"\n" +
			"--holder " + claimActorHelp,
		Example: "  slate claims extend 412\n  slate claims extend 412 --minutes 600",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validClaimMinutes(minutes); err != nil {
				return err
			}
			holder, err := claimActor(cmd, "holder", holder)
			if err != nil {
				return err
			}
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.ExtendClaim(cmd.Context(), args[0], holder, minutes, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, claimSummary("Extended", written.Claim, time.Now()))
		},
	}
	cmd.Flags().StringVar(&holder, "holder", actorIdentity(), "Who holds the claim")
	addClaimMinutesFlag(cmd, &minutes)
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newClaimsReleaseCommand() *cobra.Command {
	var (
		asJSON     bool
		releasedBy string
	)
	cmd := &cobra.Command{
		Use:   "release <record>",
		Short: "End a record's claim, handing it back unfinished",
		Long: "Ends whoever's claim it is, so a claim left by a session that died is freed\n" +
			"now rather than when it runs out. Releasing another holder's claim goes\n" +
			"through with a warning. A record nobody holds changes nothing and warns.\n" +
			"\n" +
			"--released-by " + claimActorHelp,
		Example: "  slate claims release 412",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			releasedBy, err := claimActor(cmd, "released-by", releasedBy)
			if err != nil {
				return err
			}
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.ReleaseClaim(cmd.Context(), args[0], releasedBy, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, claimSummary("Released", written.Claim, time.Now()))
		},
	}
	cmd.Flags().StringVar(&releasedBy, "released-by", actorIdentity(), "Who ended the claim")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newClaimsListCommand() *cobra.Command {
	var (
		asJSON bool
		filter api.ClaimFilter
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List claims, latest started first: the live ones by default",
		Long: "--state live is who holds what now. expired is the claims that ran out and\n" +
			"no write has ended yet. ended is history, and all is every claim.\n" +
			"\n" +
			"--holder matches the start of the holder, so claude-code/ is every attended\n" +
			"Claude Code session.",
		Example: "  slate claims list\n" +
			"  slate claims list --holder claude-code/\n" +
			"  slate claims list --record 412 --state all --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if filter.State != "" && !slices.Contains(api.ClaimStates, filter.State) {
				return goclikit.UsageError(fmt.Errorf("unknown state %q — one of: %s", filter.State, strings.Join(api.ClaimStates, ", ")))
			}
			client, err := newAPIClient(cmd.Context())
			if err != nil {
				return handleAPIError(err)
			}
			claims, err := client.ListClaims(cmd.Context(), filter, limitFlag(cmd))
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), claims)
			}
			printClaimsTable(cmd.OutOrStdout(), claims, time.Now())
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&filter.Holder, "holder", "", "Only claims whose holder starts with this")
	f.StringVar(&filter.Record, "record", "", "Only claims on this record, by number")
	f.StringVar(&filter.State, "state", "", "One of: "+strings.Join(api.ClaimStates, ", ")+" (default live)")
	addLimitFlag(cmd, &limit)
	f.BoolVar(&asJSON, "json", false, "Output claims as JSON to stdout")
	return cmd
}

func addClaimMinutesFlag(cmd *cobra.Command, target *int) {
	cmd.Flags().IntVar(target, "minutes", defaultClaimMinutes, "How long the claim holds, 1 to "+strconv.Itoa(maxClaimMinutes))
}

func validClaimMinutes(minutes int) error {
	if minutes < 1 || minutes > maxClaimMinutes {
		return goclikit.UsageError(fmt.Errorf("--minutes %d: a claim lasts 1 to %d minutes", minutes, maxClaimMinutes))
	}
	return nil
}

// claimActor is who a claim write names. A service's API takes only its own
// client id, so a service run defaults to that rather than to actorIdentity.
func claimActor(cmd *cobra.Command, flag, value string) (string, error) {
	if !cmd.Flags().Changed(flag) {
		if deployment, err := config.Load(cmd.Context(), apiOverride); config.IsService() || deployment.HandsTokens() {
			if err != nil {
				return "", handleAPIError(err)
			}
			value = deployment.ClientID.Value
		}
	}
	if strings.TrimSpace(value) == "" {
		return "", goclikit.UsageError(fmt.Errorf("--%s cannot be empty — a claim names who holds it", flag))
	}
	return value, nil
}

// claimLease is the hold's holder and length, with where the holder works.
func claimLease(cmd *cobra.Command, holder string, minutes int) (api.ClaimLease, error) {
	if err := validClaimMinutes(minutes); err != nil {
		return api.ClaimLease{}, err
	}
	holder, err := claimActor(cmd, "holder", holder)
	if err != nil {
		return api.ClaimLease{}, err
	}
	lease := api.ClaimLease{Holder: holder, Minutes: minutes, Session: nonEmpty(os.Getenv("CLAUDE_CODE_SESSION_ID"))}
	if host, err := os.Hostname(); err == nil {
		lease.Host = nonEmpty(host)
	}
	if directory, err := os.Getwd(); err == nil {
		lease.Directory = nonEmpty(directory)
	}
	return lease, nil
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// claimSummary is the line naming what a claim write did. A write that found
// nothing to hold or release has no claim, and its warning says so.
func claimSummary(verb string, claim *api.ClaimOnRecord, now time.Time) string {
	if claim == nil {
		return ""
	}
	line := fmt.Sprintf("%s #%d %s", verb, claim.Record.Number, claim.Record.Title)
	if claim.EndedTS != nil {
		return line + ", held by " + claim.Holder
	}
	return line + " as " + claim.Holder + " until " + localClock(claim.ExpiresTS, now)
}

// claimSpan says when a claim held its record and how it stopped.
func claimSpan(claim api.Claim, now time.Time) string {
	started := localClock(claim.StartedTS, now)
	switch {
	case claim.EndedTS != nil:
		span := started + " to " + localClock(*claim.EndedTS, now) + ", " + strValue(claim.Ending)
		if by := strValue(claim.EndedBy); by != "" && by != claim.Holder {
			span += " by " + by
		}
		return span
	case claim.State == api.ClaimStateExpired:
		return started + ", ran out " + localClock(claim.ExpiresTS, now)
	default:
		return started + ", holds until " + localClock(claim.ExpiresTS, now)
	}
}

func printClaimHistory(out io.Writer, claims []api.Claim, now time.Time) {
	for _, claim := range claims {
		_, _ = fmt.Fprintf(out, "  %s  %s\n", claim.Holder, claimSpan(claim, now))
	}
}

func printClaimsTable(out io.Writer, claims []api.ClaimOnRecord, now time.Time) {
	if len(claims) == 0 {
		_, _ = fmt.Fprintln(out, "No claims.")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "RECORD\tKIND\tHOLDER\tHELD\tTITLE")
	for _, claim := range claims {
		_, _ = fmt.Fprintf(tw, "#%d\t%s\t%s\t%s\t%s\n",
			claim.Record.Number, claim.Record.Kind, claim.Holder, claimSpan(claim.Claim, now), claim.Record.Title)
	}
	_ = tw.Flush()
}
