// Package cli wires the slate command tree.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/goselfupdate/autoupdate"
	"github.com/datapointchris/gotermstyle/cobrahelp"
	"github.com/spf13/cobra"
)

// version is overridden at build time via -ldflags.
var version = "dev"

// noInput is bound to the persistent --no-input flag, which forces the
// non-interactive path from a terminal. Package-level because every destructive
// verb consults it through confirm; pflag rewrites it to the default on each
// NewRootCommand, so a test never inherits the previous one's value.
var noInput bool

// apiOverride is bound to --api, the one value that outranks the config file.
// Binding it as the tree is built resets it, so a test never inherits the
// previous tree's value.
var apiOverride string

// withAPIFlag declares --api on cmd and everything under it. Only the commands
// that reach the API carry it, so one that never reads it does not offer it.
func withAPIFlag(cmd *cobra.Command) *cobra.Command {
	cmd.PersistentFlags().StringVar(&apiOverride, "api", "",
		"Use this API instead of the config file's; it must trust the file's login server")
	return cmd
}

// The root's help sections: the commands that take no noun, the records work
// is planned in, the commands that do it, then what sets the CLI and its
// vocabulary up.
const (
	groupNoNoun   = "no-noun"
	groupPlanning = "planning"
	groupDoing    = "doing"
	groupSetup    = "setup"
)

// Within a namespace whose verbs both read and change something, the verbs
// are split by which they do, so the half that changes something is visible
// without reading each Short.
const (
	groupReading  = "reading"
	groupChanging = "changing"
)

// splitReadingFromChanging gives cmd the two sections its verbs go under.
func splitReadingFromChanging(cmd *cobra.Command) {
	cmd.AddGroup(
		&cobra.Group{ID: groupReading, Title: "Reading commands:"},
		&cobra.Group{ID: groupChanging, Title: "Changing commands:"},
	)
}

// inGroup puts each of cmds under the section id and returns them, for an
// AddCommand list.
func inGroup(id string, cmds ...*cobra.Command) []*cobra.Command {
	for _, cmd := range cmds {
		cmd.GroupID = id
	}
	return cmds
}

// exitCode lets a command choose the process exit code without Execute printing
// an "error:" line — used by `auth status` to report "not logged in" (exit 1) as
// a valid state, not a failure.
type exitCode int

func (e exitCode) Error() string { return "" }

// The statuses a composer branches on, beside 1 for a refusal and 2 for usage.
const (
	statusNoAnswer          = 4
	statusCredentialRefused = 5
)

// runRoot answers `slate` bare with its help, and refuses a word naming no
// command the way every namespace does. `slate version` is refused too, with
// the flag that prints the version named under it, because the version is a
// flag rather than a command.
func runRoot(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	refused := goclikit.UnknownCommand(cmd, args[0])
	if args[0] == "version" {
		return fmt.Errorf("%w\n\nThe version is a flag: %s --version", refused, cmd.CommandPath())
	}
	return refused
}

func NewRootCommand() *cobra.Command {
	root := goclikit.AsNamespace(&cobra.Command{
		Use:   "slate",
		Short: "Work tracker built for coding agents",
		Long: "slate tracks software work for the agents and people doing it.\n" +
			"\n" +
			"The noun comes first and the verb last, so `slate issues list` becomes\n" +
			"`slate issues edit` by changing the last word. These take no noun:\n" +
			"`slate next` is the order to take the work in, `slate show <number>`\n" +
			"and `slate search <words>` read every kind, `slate totals` sums the work\n" +
			"over any record, worklog, repo or window, and `slate tui` opens a live board\n" +
			"with a tab per view.\n" +
			"\n" +
			"Ideas, initiatives, issues and pursuits share one number sequence, so a\n" +
			"number names one record whatever its kind. A worklog is named under its\n" +
			"record's number: 412.2 is the second worklog on #412.\n" +
			"\n" +
			"Nothing happens that a command does not print. A write names every change\n" +
			"it made beyond the one asked for, and every warning it went past. A read that\n" +
			"leaves rows out by default says which, and how to include them. Every read\n" +
			"and write takes --json.\n" +
			"\n" +
			"Authenticate once with `slate auth login <url>`. `slate auth status` shows\n" +
			"where slate is pointed. Run any partial command bare to see what comes next.",
		Example: "  slate next --limit 1\n" +
			"  slate show 42\n" +
			"  slate claims hold 42\n" +
			"  slate search retry upload\n" +
			"  slate issues create --title \"Upload gives up on a 503 with no retry\" --type bug",
		Version:       version,
		SilenceUsage:  true, // usage is shown deliberately, not on every runtime error
		SilenceErrors: true, // Execute prints errors itself, to stderr
		// ArbitraryArgs lets an unknown top-level command (`slate nope`) reach the
		// namespace's own refusal, marked as a usage error (exit 2). Cobra's
		// default root validator (legacyArgs) instead returns its own unmarked
		// "unknown command" error (exit 1). Subcommands, having a parent, are
		// exempt from that validator.
		Args: cobra.ArbitraryArgs,
		RunE: runRoot,
	})
	// Flag mistakes are marked goclikit.ErrUsage, so they exit 2. Inherited by subcommands.
	// goclikit.Execute composes with this rather than replacing it, and keeping
	// it here is what makes the tree self-classifying for anything driving
	// NewRootCommand directly. An unknown flag that is a value of one of the
	// command's flags also names that flag.
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return goclikit.UsageError(explainUnknownFlag(cmd, err))
	})
	cobrahelp.Install(root)
	showServiceScopesInHelp(root)

	root.PersistentFlags().BoolVar(&noInput, "no-input", false,
		"Never prompt; fail naming the flag that would have answered")

	// Free -v for a future --verbose flag: cobra's auto version flag claims -v,
	// but the CLI convention reserves -v for verbose and -V/--version for
	// version. Drop the auto shorthand so --version stays long-only for now.
	root.InitDefaultVersionFlag()
	if f := root.Flags().Lookup("version"); f != nil {
		f.Shorthand = ""
	}

	root.AddGroup(
		&cobra.Group{ID: groupNoNoun, Title: "Start here:"},
		&cobra.Group{ID: groupPlanning, Title: "Planning:"},
		&cobra.Group{ID: groupDoing, Title: "Doing:"},
		&cobra.Group{ID: groupSetup, Title: "Setup:"},
	)
	root.SetCompletionCommandGroupID(groupSetup)
	root.AddCommand(inGroup(groupNoNoun,
		withAPIFlag(goclikit.WithRecoveryHints(newNextCommand(), initiativeHints...)),
		withAPIFlag(newShowCommand()),
		withAPIFlag(newSearchCommand()),
		withAPIFlag(newTotalsCommand()),
		withAPIFlag(newTUICommand()),
	)...)
	root.AddCommand(inGroup(groupPlanning,
		withAPIFlag(newPursuitsCommand()),
		withAPIFlag(newIdeasCommand()),
		withAPIFlag(newInitiativesCommand()),
		withAPIFlag(newIssuesCommand()),
		withAPIFlag(newDependenciesCommand()),
	)...)
	root.AddCommand(inGroup(groupDoing,
		withAPIFlag(newClaimsCommand()),
		withAPIFlag(newWorklogsCommand()),
		withAPIFlag(newSessionsCommand()),
		withAPIFlag(newDocumentsCommand()),
		withAPIFlag(newCommentsCommand()),
	)...)
	root.AddCommand(inGroup(groupSetup,
		withAPIFlag(newLabelsCommand()),
		withAPIFlag(newTagsCommand()),
		withAPIFlag(newVocabularyCommand()),
		newAuthCommand(),
		newConfigCommand(),
		newUpdateCommand(),
	)...)
	return root
}

// Execute runs the command tree, prints any error to stderr, and returns the
// process exit code.
func Execute() int {
	err := run(NewRootCommand(), autoupdate.Config{Update: updateConfig()})

	// An exitCode carries its own code and an empty message, so it is not printed
	// as an "error:" line — it reports a valid non-success state (e.g. "not logged
	// in") rather than a failure.
	// An ErrReported failure is already on screen: `update` writes its own ✓/✗
	// line, and a command given none of its arguments prints its help. Printing
	// here would report it twice.
	var ec exitCode
	if err != nil && !errors.As(err, &ec) && !errors.Is(err, goclikit.ErrReported) {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	return exitCodeFor(err)
}

// run drives root through the shared bootstrap and returns its error.
//
// Separate from Execute, and taking the update config, so a test can drive the
// real tree with the version check suppressed and read the error rather than an
// exit code. WithNotFound is the whole of the not-found feature's wiring: drop
// it and every 404 prints the API's status line again, which is what
// TestA404FromTheRealTreeCarriesItsRecoveryHints catches.
func run(root *cobra.Command, config autoupdate.Config) error {
	return goclikit.Execute(withParentTrace(context.Background()), root, config, goclikit.WithNotFound(notFound))
}

// exitCodeFor maps a command error to a process exit code. 0 is success, and 2 a
// usage mistake. 4 means no answer came, and 5 a credential a person has to fix,
// and neither says anything about the request. An explicit exitCode keeps its own
// value, and anything else the API or the command refused is 1.
func exitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	var ec exitCode
	if errors.As(err, &ec) {
		return int(ec)
	}
	// A usage mistake is marked either by goclikit, for a line cobra rejects
	// before any RunE runs, or by the command that refused it.
	if errors.Is(err, goclikit.ErrUsage) {
		return 2
	}
	var credential credentialRefused
	if errors.As(err, &credential) {
		return statusCredentialRefused
	}
	if noAnswer(err) {
		return statusNoAnswer
	}
	return 1
}
