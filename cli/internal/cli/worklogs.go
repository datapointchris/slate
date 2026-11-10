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

	"github.com/datapointchris/slate/cli/internal/api"
)

var worklogHints = []string{
	"List the live worklogs: slate worklogs list",
	"Name the worklog this session is in: slate worklogs current",
}

const agentSessionHelp = "The agent session (default $" + claudeCodeSessionEnv + ")"

func newWorklogsCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "worklogs",
		Short: "Read and record each go at a record: its sessions, commits, pull requests and ending",
		Long: "A worklog is one go at one record, named under its number: 1991.2 is the\n" +
			"second worklog on 1991. Holding a claim opens one and prints its name, and the\n" +
			"claim ending ends it. `start` opens one with no claim, for discussing an idea or\n" +
			"an initiative, and `finish` ends one by hand.\n" +
			"\n" +
			"A session joins worklogs for spans of time, one at a time. Holding a claim,\n" +
			"resuming one, starting a worklog or joining one moves it, and the answer names\n" +
			"the worklog it left. `current` prints the worklog the session is in, for a\n" +
			"commit hook to stamp as the trailer `Worklog: 1991.2`. `link` records the\n" +
			"commit, pull request or landing it produced.",
		Example: "  slate worklogs current                                             the worklog this session is in\n" +
			"  slate worklogs link --commit \"$(git rev-parse HEAD)\" --repo slate  record the commit it made\n" +
			"  slate worklogs list                                                what is under way now\n" +
			"  slate worklogs show 1991.2                                         its sessions, links and claim",
	})
	goclikit.WithRecoveryHints(cmd, worklogHints...)
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupReading,
		newWorklogsListCommand(),
		goclikit.WithRecoveryHints(newWorklogsShowCommand(), worklogHints...),
		newWorklogsCurrentCommand(),
	)...)
	cmd.AddCommand(inGroup(groupChanging,
		goclikit.WithRecoveryHints(newWorklogsStartCommand(), worklogHints...),
		goclikit.WithRecoveryHints(newWorklogsJoinCommand(), worklogHints...),
		goclikit.WithRecoveryHints(newWorklogsLinkCommand(), worklogHints...),
		goclikit.WithRecoveryHints(newWorklogsEditCommand(), worklogHints...),
		goclikit.WithRecoveryHints(newWorklogsFinishCommand(), worklogHints...),
	)...)
	return cmd
}

func newWorklogsListCommand() *cobra.Command {
	var (
		asJSON bool
		filter api.WorklogFilter
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List worklogs, latest started first: the live ones by default",
		Long: "--state live is what is under way now. expired is the worklogs whose claim ran\n" +
			"out and no write has ended yet. ended is history, and all is every worklog.\n" +
			"STAGE is the furthest it reached: started, planned once it wrote a plan\n" +
			"document, then committed, pull_request and landed as its links reach them.",
		Example: "  slate worklogs list\n" +
			"  slate worklogs list --record 1991 --state all\n" +
			"  slate worklogs list --session \"$CLAUDE_CODE_SESSION_ID\" --state all --json",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if filter.State != "" && !slices.Contains(api.WorklogStates, filter.State) {
				return goclikit.UsageError(fmt.Errorf("unknown state %q — one of: %s", filter.State, strings.Join(api.WorklogStates, ", ")))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			worklogs, err := client.ListWorklogs(cmd.Context(), filter, limitFlag(cmd))
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), worklogs)
			}
			printWorklogsTable(cmd.OutOrStdout(), worklogs, time.Now())
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&filter.Record, "record", "", "Only worklogs on this record, by number")
	f.StringVar(&filter.Session, "session", "", "Only worklogs this agent session joined")
	f.StringVar(&filter.State, "state", "", "One of: "+strings.Join(api.WorklogStates, ", ")+" (default live)")
	addLimitFlag(cmd, &limit)
	f.BoolVar(&asJSON, "json", false, "Output worklogs as JSON to stdout")
	return cmd
}

func newWorklogsShowCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:         "show <worklog>",
		Short:       "Show a worklog with its sessions' spans, links, claim and waits",
		Example:     "  slate worklogs show 1991.2\n  slate worklogs show 1991.2 --json",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			worklog, err := client.GetWorklog(cmd.Context(), args[0])
			if err != nil {
				return handleArgumentAPIError(err)
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), worklog)
			}
			printWorklogDetail(cmd.OutOrStdout(), worklog, time.Now())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the worklog as JSON to stdout")
	return cmd
}

func newWorklogsCurrentCommand() *cobra.Command {
	var (
		asJSON  bool
		session string
	)
	cmd := &cobra.Command{
		Use:   "current",
		Short: "Print the name of the worklog this session is in, or nothing",
		Long: "Prints the live worklog's name alone on a line, for a commit-msg hook to stamp\n" +
			"as the trailer `Worklog: <name>`. A session in no worklog, or in one whose claim\n" +
			"ran out or ended, prints nothing and exits 0. So does a run naming no session.\n" +
			"--json prints the whole worklog, or null.",
		Example: "  slate worklogs current\n" +
			"  name=$(slate worklogs current) && [ -n \"$name\" ] &&\n" +
			"    git interpret-trailers --in-place --if-exists doNothing --trailer \"Worklog: $name\" \"$1\"",
		Args:        cobra.NoArgs,
		Annotations: scopesForService(scopeRecordsRead),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var worklog *api.Worklog
			if strings.TrimSpace(session) != "" {
				client, err := newAPIClient(cmd)
				if err != nil {
					return handleAPIError(err)
				}
				if worklog, err = client.CurrentWorklog(cmd.Context(), session); err != nil {
					return handleArgumentAPIError(err)
				}
			}
			if asJSON {
				return encodeJSON(cmd.OutOrStdout(), worklog)
			}
			if worklog != nil {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), worklog.Name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&session, "session", os.Getenv(claudeCodeSessionEnv), agentSessionHelp)
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the worklog as JSON to stdout, or null")
	return cmd
}

func newWorklogsStartCommand() *cobra.Command {
	var (
		asJSON   bool
		session  string
		openedBy string
		step     string
	)
	cmd := &cobra.Command{
		Use:   "start <record>",
		Short: "Open a worklog on a record with no claim, and put this session in it",
		Long: "For a go at a record nobody claims, such as discussing an idea or planning an\n" +
			"initiative. The session joins it, leaving any other worklog it was in, which\n" +
			"is printed under the line. Each worklog already open on the record goes\n" +
			"through with a warning, since joining it may be what you meant. Completing,\n" +
			"canceling, dropping or promoting the record ends it, as does `finish`.\n" +
			"\n" +
			stepHelp + "\n" +
			"\n" +
			"--opened-by " + narrowedActorHelp,
		Example: "  slate worklogs start 1860\n" +
			"  slate worklogs start 2072 --step build",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeWorklogsWrite),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validStep(step); err != nil {
				return err
			}
			openedBy, err := narrowedActor(cmd, "opened-by", openedBy)
			if err != nil {
				return err
			}
			in := api.WorklogStartInput{Record: args[0], OpenedBy: openedBy, AgentPlace: agentPlace(session)}
			in.Step = nonEmpty(step)
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.StartWorklog(cmd.Context(), in, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, worklogLine("Started", written.Worklog))
		},
	}
	f := cmd.Flags()
	addStepFlag(cmd, &step, "The step of the record's life this worklog performs")
	f.StringVar(&session, "session", os.Getenv(claudeCodeSessionEnv), agentSessionHelp+"; empty puts no session in it")
	f.StringVar(&openedBy, "opened-by", actorIdentity(), "Who opened it")
	f.BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newWorklogsJoinCommand() *cobra.Command {
	var (
		asJSON  bool
		session string
	)
	cmd := &cobra.Command{
		Use:   "join <worklog>",
		Short: "Put this session in a worklog, leaving the one it was in",
		Long: "A session is in one worklog at a time. Joining ends its span in any other,\n" +
			"which is printed under the line. Use it to work a worklog another session\n" +
			"opened, or one `start` opened. Holding or resuming a claim returns the session\n" +
			"to that claim's worklog. Refused when the worklog ended, or its claim ran out\n" +
			"or ended. A service joins only a worklog its client opened, unless its token\n" +
			"holds slate.worklogs.observe, which is for a session that commits nothing.",
		Example:     "  slate worklogs join 1991.2",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeWorklogsWrite),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(session) == "" {
				return goclikit.UsageError(errors.New("--session is required outside Claude Code — a worklog is joined by a session"))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.JoinWorklog(cmd.Context(), args[0], agentPlace(session), issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, worklogLine("Session "+session+" is in", written.Worklog))
		},
	}
	cmd.Flags().StringVar(&session, "session", os.Getenv(claudeCodeSessionEnv), agentSessionHelp)
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newWorklogsLinkCommand() *cobra.Command {
	var (
		asJSON          bool
		commit, pr, ref string
		repo, session   string
		linkedBy        string
	)
	cmd := &cobra.Command{
		Use:   "link [<worklog>] (--commit <sha> | --pr <url> | --landed <ref>)",
		Short: "Record a commit, pull request or landing a worklog produced",
		Long: "slate stores the ref as given and never follows it. A landing is a commit or a\n" +
			"pull request reaching the main branch. A ref the worklog already links changes\n" +
			"nothing and warns. A worklog that ended still takes a link, since a pull request\n" +
			"lands after the claim that made it is done.\n" +
			"\n" +
			"With no worklog named, it links to the one this session is in, as `current`\n" +
			"prints it, and a session in none is refused. A post-commit hook reads `current`\n" +
			"first and links only when it printed a name, so a commit outside any worklog\n" +
			"prints nothing.\n" +
			"\n" +
			"--linked-by " + narrowedActorHelp,
		Example: "  slate worklogs link --commit \"$(git rev-parse HEAD)\" --repo slate\n" +
			"  name=$(slate worklogs current) && [ -n \"$name\" ] &&\n" +
			"    slate worklogs link \"$name\" --commit \"$(git rev-parse HEAD)\" >/dev/null\n" +
			"  slate worklogs link 1991.2 --pr https://github.com/datapointchris/slate/pull/3\n" +
			"  slate worklogs link 1991.2 --landed 4fca8f1 --repo slate",
		Args:        cobra.MaximumNArgs(1),
		Annotations: scopesForService(scopeRecordsRead, scopeWorklogsWrite),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			var named []string
			in := api.WorklogLinkInput{Session: nonEmpty(session)}
			for _, flag := range []struct{ name, kind, value string }{
				{"commit", api.WorklogLinkCommit, commit},
				{"pr", api.WorklogLinkPullRequest, pr},
				{"landed", api.WorklogLinkLanding, ref},
			} {
				if f.Changed(flag.name) {
					named = append(named, "--"+flag.name)
					in.Kind, in.Ref = flag.kind, strings.TrimSpace(flag.value)
				}
			}
			if len(named) != 1 {
				return goclikit.UsageError(errors.New("pass exactly one of --commit, --pr or --landed"))
			}
			if in.Ref == "" {
				return goclikit.UsageError(fmt.Errorf("%s cannot be empty — a link names what it links", named[0]))
			}
			if err := validateRepoFlag(cmd, repo); err != nil {
				return err
			}
			in.Repo = nonEmpty(repo)
			linkedBy, err := narrowedActor(cmd, "linked-by", linkedBy)
			if err != nil {
				return err
			}
			in.LinkedBy = linkedBy
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			var name string
			if len(args) == 1 {
				name = args[0]
			} else {
				current, err := sessionWorklog(cmd, client, session)
				if err != nil {
					return err
				}
				name = current.Name
			}
			written, err := client.LinkWorklog(cmd.Context(), name, in, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			summary := fmt.Sprintf("Worklog %s links %s %s", written.Worklog.Name, strings.ReplaceAll(in.Kind, "_", " "), in.Ref)
			return reportWrite(cmd, asJSON, written, written.WriteEffects, summary)
		},
	}
	f := cmd.Flags()
	f.StringVar(&commit, "commit", "", "A commit's sha")
	f.StringVar(&pr, "pr", "", "A pull request's URL")
	f.StringVar(&ref, "landed", "", "The commit or pull request that reached the main branch")
	f.StringVar(&repo, "repo", "", "The repo it is in")
	f.StringVar(&session, "session", os.Getenv(claudeCodeSessionEnv), agentSessionHelp+", which made it")
	f.StringVar(&linkedBy, "linked-by", actorIdentity(), "Who linked it")
	f.BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

// sessionWorklog is the live worklog the session is in, refused when it is in
// none, since a link with nowhere to go would be dropped.
func sessionWorklog(cmd *cobra.Command, client *api.Client, session string) (*api.Worklog, error) {
	if strings.TrimSpace(session) == "" {
		return nil, goclikit.UsageError(errors.New("name a worklog — no session is running, so there is no current one"))
	}
	worklog, err := client.CurrentWorklog(cmd.Context(), session)
	if err != nil {
		return nil, handleArgumentAPIError(err)
	}
	if worklog == nil {
		return nil, goclikit.UsageError(fmt.Errorf("name a worklog — session %s is in none", session))
	}
	return worklog, nil
}

func newWorklogsEditCommand() *cobra.Command {
	var (
		asJSON bool
		step   string
	)
	estimate := map[string]*int{}
	cmd := &cobra.Command{
		Use:   "edit <worklog> [--step <step>] [--estimate-<kind> <tokens>]...",
		Short: "Set the step of its record's life a worklog performed, and its token estimate by kind",
		Long: stepHelp + " --step \"\" clears it, so a wrong step is corrected or taken off.\n" +
			"\n" +
			"Each --estimate flag sets one kind of the tokens the work is expected to\n" +
			"spend, and keeps the kinds it does not name. An empty value clears that kind,\n" +
			"which differs from an estimate of zero. `slate totals <record>` prints the\n" +
			"estimate of the record's latest preflight worklog beside what it spent, so\n" +
			"an estimate on a worklog of any other step warns.",
		Example: "  slate worklogs edit 1991.2 --step design\n" +
			"  slate worklogs edit 2804.3 --estimate-output 900000 --estimate-cache-read 40000000",
		SuggestFor:  editMistakenFor,
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeWorklogsWrite),
		RunE: func(cmd *cobra.Command, args []string) error {
			var edit api.WorklogEdit
			if cmd.Flags().Changed("step") {
				if err := validStep(step); err != nil {
					return err
				}
				edit.Step = &step
			}
			if len(estimate) > 0 {
				edit.Estimate = estimate
			}
			if edit.Step == nil && edit.Estimate == nil {
				return goclikit.UsageError(errors.New("nothing to change — pass --step or an --estimate flag"))
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.EditWorklog(cmd.Context(), args[0], edit)
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, worklogLine("Updated", written.Worklog))
		},
	}
	addStepFlag(cmd, &step, "The step it performed (empty clears)")
	for _, kind := range api.TokenKinds {
		noun := strings.ReplaceAll(kind, "_", " ")
		cmd.Flags().Var(estimateValue{kind: kind, estimate: estimate}, estimateFlag(kind), "The "+noun+" tokens it expects to spend (empty clears)")
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func estimateFlag(kind string) string {
	return "estimate-" + strings.ReplaceAll(kind, "_", "-")
}

// estimateValue parses one --estimate-<kind> flag into the edit's estimate: a
// token count, or empty to clear the kind. A kind no flag set stays out of it,
// so the edit keeps that kind.
type estimateValue struct {
	kind     string
	estimate map[string]*int
}

func (v estimateValue) String() string {
	if tokens := v.estimate[v.kind]; tokens != nil {
		return strconv.Itoa(*tokens)
	}
	return ""
}

func (v estimateValue) Type() string { return "tokens" }

func (v estimateValue) Set(raw string) error {
	if raw == "" {
		v.estimate[v.kind] = nil
		return nil
	}
	tokens, err := strconv.Atoi(raw)
	if err != nil || tokens < 0 {
		return fmt.Errorf("invalid estimate %q: a token count is a whole number, zero or more, or empty to clear it", raw)
	}
	v.estimate[v.kind] = &tokens
	return nil
}

func newWorklogsFinishCommand() *cobra.Command {
	var (
		asJSON     bool
		finishedBy string
	)
	cmd := &cobra.Command{
		Use:   "finish <worklog>",
		Short: "End a worklog by hand, with every span still open in it",
		Long: "Its outcome is finished. A claim whose hold opened it keeps holding its record,\n" +
			"with a warning: release the claim to let the record go. A worklog already\n" +
			"ended changes nothing and warns.\n" +
			"\n" +
			"--finished-by " + narrowedActorHelp,
		Example:     "  slate worklogs finish 1860.1",
		Args:        cobra.ExactArgs(1),
		Annotations: scopesForService(scopeWorklogsWrite),
		RunE: func(cmd *cobra.Command, args []string) error {
			finishedBy, err := narrowedActor(cmd, "finished-by", finishedBy)
			if err != nil {
				return err
			}
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, err := client.FinishWorklog(cmd.Context(), args[0], finishedBy, issueZone())
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, written.WriteEffects, worklogLine("Finished", written.Worklog))
		},
	}
	cmd.Flags().StringVar(&finishedBy, "finished-by", actorIdentity(), "Who finished it")
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

// stepHelp says what a step is, for each command whose --step names one.
const stepHelp = "--step names the step of the record's life the worklog performs, which\n" +
	"`slate vocabulary` lists in order. A step is checked on the record once that\n" +
	"worklog ends, unless its claim ran out or the record was dropped or canceled.\n" +
	"`slate show` of an initiative prints its steps with its idea's."

func addStepFlag(cmd *cobra.Command, step *string, usage string) {
	cmd.Flags().StringVar(step, "step", "", usage+", one of: "+strings.Join(api.WorklogSteps, ", "))
	withFlagValues(cmd, "step", api.WorklogSteps...)
}

func validStep(step string) error {
	if step == "" || slices.Contains(api.WorklogSteps, step) {
		return nil
	}
	return goclikit.UsageError(fmt.Errorf("unknown step %q — one of: %s", step, strings.Join(api.WorklogSteps, ", ")))
}

func worklogLine(verb string, worklog api.Worklog) string {
	return fmt.Sprintf("%s worklog %s, #%d %s", verb, worklog.Name, worklog.RecordNumber, worklog.Record.Title)
}

// worklogState is the state with the outcome an ended worklog reached.
func worklogState(worklog api.Worklog) string {
	if outcome := strValue(worklog.Outcome); outcome != "" {
		return worklog.State + " " + outcome
	}
	return worklog.State
}

// spanTimes says when a span or wait began, and when it ended or that it has not.
func spanTimes(started time.Time, ended *time.Time, now time.Time, ongoing string) string {
	if ended == nil {
		return localClock(started, now) + ", " + ongoing
	}
	return localClock(started, now) + " to " + localClock(*ended, now)
}

func distinctSessions(spans []api.WorklogSpan) int {
	seen := map[string]bool{}
	for _, span := range spans {
		seen[span.Session] = true
	}
	return len(seen)
}

func printWorklogsTable(out io.Writer, worklogs []api.Worklog, now time.Time) {
	if len(worklogs) == 0 {
		_, _ = fmt.Fprintln(out, "No worklogs.")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "WORKLOG\tSTATE\tSTEP\tSTAGE\tSTARTED\tSESSIONS\tTITLE")
	for _, worklog := range worklogs {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n", worklog.Name, worklogState(worklog), orDash(strValue(worklog.Step)), worklog.Stage,
			localClock(worklog.StartedTS, now), distinctSessions(worklog.Spans), worklog.Record.Title)
	}
	_ = tw.Flush()
}

func printWorklogDetail(out io.Writer, worklog api.Worklog, now time.Time) {
	p := gotermstyle.PaletteFor(out)
	_, _ = fmt.Fprintf(out, "%s  %s %s\n", p.Cyan(worklog.Name), p.Cyan("#"+strconv.Itoa(worklog.RecordNumber)), p.Bold(worklog.Record.Title))
	_, _ = fmt.Fprintf(out, "  state:     %s, %s\n", worklogState(worklog), worklog.Stage)
	_, _ = fmt.Fprintf(out, "  opened:    %s by %s\n", localClock(worklog.StartedTS, now), worklog.OpenedBy)
	if worklog.Claim != nil {
		_, _ = fmt.Fprintf(out, "  claim:     %s\n", claimSpan(*worklog.Claim, now))
	}
	if step := strValue(worklog.Step); step != "" {
		_, _ = fmt.Fprintf(out, "  step:      %s\n", step)
	}
	if worklog.EndedTS != nil {
		ended := localClock(*worklog.EndedTS, now)
		if by := strValue(worklog.EndedBy); by != "" {
			ended += " by " + by
		}
		_, _ = fmt.Fprintf(out, "  ended:     %s\n", ended)
	}
	if worklog.Tokens != nil {
		_, _ = fmt.Fprintf(out, "  tokens:    %s\n", tokensLine(*worklog.Tokens))
	}
	if worklog.Estimate != nil {
		_, _ = fmt.Fprintf(out, "  estimate:  %s\n", estimateLine(*worklog.Estimate))
	}
	if len(worklog.Spans) > 0 {
		_, _ = fmt.Fprintf(out, "\nSessions (%d):\n", len(worklog.Spans))
		for _, span := range worklog.Spans {
			line := "  " + span.Session + "  " + spanTimes(span.StartedTS, span.EndedTS, now, "still in it")
			if directory := strValue(span.Directory); directory != "" {
				line += "  " + directory
			}
			_, _ = fmt.Fprintln(out, line)
		}
	}
	if len(worklog.Links) > 0 {
		_, _ = fmt.Fprintf(out, "\nLinks (%d):\n", len(worklog.Links))
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		for _, link := range worklog.Links {
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s by %s\n",
				link.Kind, link.Ref, orDash(strValue(link.Repo)), localClock(link.LinkedTS, now), link.LinkedBy)
		}
		_ = tw.Flush()
	}
	if len(worklog.Waits) > 0 {
		_, _ = fmt.Fprintf(out, "\nWaits (%d):\n", len(worklog.Waits))
		for _, wait := range worklog.Waits {
			_, _ = fmt.Fprintf(out, "  %s  %s\n", wait.WaitingOn, spanTimes(wait.StartedTS, wait.EndedTS, now, "still waiting"))
		}
	}
	if len(worklog.DocumentVersions) > 0 {
		_, _ = fmt.Fprintf(out, "\nDocuments written (%d):\n", len(worklog.DocumentVersions))
		for _, version := range worklog.DocumentVersions {
			_, _ = fmt.Fprintf(out, "  %s version %d  %s  %s\n", version.Address, version.Version, version.Kind, localClock(version.WrittenTS, now))
		}
	}
}
