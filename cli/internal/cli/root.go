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

// The root's help sections: the work, then the commands that set the CLI up.
const (
	groupWork  = "work"
	groupSetup = "setup"
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

func NewRootCommand() *cobra.Command {
	root := goclikit.AsNamespace(&cobra.Command{
		Use:   "slate",
		Short: "Work tracker built for coding agents",
		Long: "slate tracks software work for the agents and people doing it: the ideas\n" +
			"nobody has taken on yet, issues, the initiatives that group them, their\n" +
			"labels, dependencies and comments, and the claims that say who is working\n" +
			"what. `slate issues next` is the order to take the work in.\n" +
			"\n" +
			"The noun comes first and the verb last: `slate issues list` becomes\n" +
			"`slate issues edit`. Every read and write takes --json. Run any partial command\n" +
			"bare to see what comes next. Authenticate once with `slate auth login <url>`;\n" +
			"`slate auth status` shows where slate is pointed.",
		Example: "  slate issues next --limit 1\n" +
			"  slate issues show 42\n" +
			"  slate claims hold 42\n" +
			"  slate issues create --title \"Retry the upload on a 503\" --type bug",
		Version:       version,
		SilenceUsage:  true, // usage is shown deliberately, not on every runtime error
		SilenceErrors: true, // Execute prints errors itself, to stderr
		// ArbitraryArgs lets an unknown top-level command (`slate nope`) reach the
		// namespace's own refusal, marked as a usage error (exit 2). Cobra's
		// default root validator (legacyArgs) instead returns its own unmarked
		// "unknown command" error (exit 1). Subcommands, having a parent, are
		// exempt from that validator.
		Args: cobra.ArbitraryArgs,
	})
	// Flag mistakes are marked goclikit.ErrUsage, so they exit 2. Inherited by subcommands.
	// goclikit.Execute composes with this rather than replacing it, and keeping
	// it here is what makes the tree self-classifying for anything driving
	// NewRootCommand directly.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return goclikit.UsageError(err)
	})
	cobrahelp.Install(root)

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
		&cobra.Group{ID: groupWork, Title: "Work commands:"},
		&cobra.Group{ID: groupSetup, Title: "Setup commands:"},
	)
	root.SetCompletionCommandGroupID(groupSetup)
	root.AddCommand(inGroup(groupWork,
		withAPIFlag(newIssuesCommand()),
		withAPIFlag(newIdeasCommand()),
		withAPIFlag(newClaimsCommand()),
	)...)
	root.AddCommand(inGroup(groupSetup, newAuthCommand(), newConfigCommand(), newUpdateCommand())...)
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
	return goclikit.Execute(context.Background(), root, config, goclikit.WithNotFound(notFound))
}

// exitCodeFor maps a command error to a process exit code: 0 success, 2 for a
// usage mistake (bad flag/args), an explicit exitCode's own value, else 1 for a
// runtime failure. Pure so it can be unit-tested without running the tree.
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
	return 1
}
