package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

// pinRecord pins one kind's record, and names it for the line the command prints.
type pinRecord func(cmd *cobra.Command, client *api.Client, ref string, pin api.PinInput) (any, api.WriteEffects, string, error)

// unpinRecord unpins one kind's record, and names it the same way.
type unpinRecord func(cmd *cobra.Command, client *api.Client, ref string) (any, api.WriteEffects, string, error)

// newPinCommand pins a record of one kind. Every kind's pin reads the same, so
// each kind's command shares this one.
func newPinCommand(kind, closes, example string, pin pinRecord) *cobra.Command {
	var (
		session string
		asJSON  bool
	)
	cmd := &cobra.Command{
		Use:   "pin <" + kind + ">",
		Short: "Hold an " + kind + " at the top of `slate next`, above every priority",
		Long: "A pin is the one way work crosses a priority. It stands until `unpin`, or until\n" +
			"the " + kind + " " + closes + ", and whatever the pinned work waits on sorts just ahead\n" +
			"of it. Pinned records sort among themselves by priority, then rank. Only a\n" +
			"person reaches this, so an automated order never pins its own favorite.",
		Example:     example,
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			place := agentPlace(session)
			written, effects, pinned, err := pin(cmd, client, args[0], api.PinInput{Session: place.Session, Host: place.Host})
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, effects, fmt.Sprintf("Pinned %s", pinned))
		},
	}
	cmd.Flags().StringVar(&session, "session", os.Getenv(claudeCodeSessionEnv), agentSessionHelp)
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

// newUnpinCommand unpins a record of one kind.
func newUnpinCommand(kind, example string, unpin unpinRecord) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:         "unpin <" + kind + ">",
		Short:       "Return an " + kind + " to the place its priority and rank give it",
		Example:     example,
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			written, effects, unpinned, err := unpin(cmd, client, args[0])
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, effects, fmt.Sprintf("Unpinned %s", unpinned))
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

func newIssuesPinCommand() *cobra.Command {
	return newPinCommand("issue", "closes", "  slate issues pin 412",
		func(cmd *cobra.Command, client *api.Client, ref string, pin api.PinInput) (any, api.WriteEffects, string, error) {
			written, err := client.PinIssue(cmd.Context(), ref, pin, issueZone())
			return written, written.WriteEffects, fmt.Sprintf("#%d %s", written.Issue.Number, written.Issue.Title), err
		})
}

func newIssuesUnpinCommand() *cobra.Command {
	return newUnpinCommand("issue", "  slate issues unpin 412",
		func(cmd *cobra.Command, client *api.Client, ref string) (any, api.WriteEffects, string, error) {
			written, err := client.UnpinIssue(cmd.Context(), ref, issueZone())
			return written, written.WriteEffects, fmt.Sprintf("#%d %s", written.Issue.Number, written.Issue.Title), err
		})
}

func newInitiativesPinCommand() *cobra.Command {
	return newPinCommand("initiative", "finishes or is shelved", "  slate initiatives pin \"Ship the tracker\"",
		func(cmd *cobra.Command, client *api.Client, ref string, pin api.PinInput) (any, api.WriteEffects, string, error) {
			written, err := client.PinInitiative(cmd.Context(), ref, pin)
			return written, written.WriteEffects, fmt.Sprintf("#%d %s", written.Initiative.Number, written.Initiative.Name), err
		})
}

func newInitiativesUnpinCommand() *cobra.Command {
	return newUnpinCommand("initiative", "  slate initiatives unpin \"Ship the tracker\"",
		func(cmd *cobra.Command, client *api.Client, ref string) (any, api.WriteEffects, string, error) {
			written, err := client.UnpinInitiative(cmd.Context(), ref)
			return written, written.WriteEffects, fmt.Sprintf("#%d %s", written.Initiative.Number, written.Initiative.Name), err
		})
}

func newIdeasPinCommand() *cobra.Command {
	return newPinCommand("idea", "is promoted, which moves the pin to its initiative", "  slate ideas pin 1530",
		func(cmd *cobra.Command, client *api.Client, ref string, pin api.PinInput) (any, api.WriteEffects, string, error) {
			written, err := client.PinIdea(cmd.Context(), ref, pin)
			return written, written.WriteEffects, fmt.Sprintf("#%d %s", written.Idea.Number, written.Idea.Title), err
		})
}

func newIdeasUnpinCommand() *cobra.Command {
	return newUnpinCommand("idea", "  slate ideas unpin 1530",
		func(cmd *cobra.Command, client *api.Client, ref string) (any, api.WriteEffects, string, error) {
			written, err := client.UnpinIdea(cmd.Context(), ref)
			return written, written.WriteEffects, fmt.Sprintf("#%d %s", written.Idea.Number, written.Idea.Title), err
		})
}
