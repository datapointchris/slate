package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
)

// newPinCommand pins a record of one kind. Every kind's pin reads the same, so
// each kind's command shares this one.
func newPinCommand(kind, closes, example string) *cobra.Command {
	var (
		session string
		asJSON  bool
	)
	cmd := &cobra.Command{
		Use:   "pin <" + kind + ">",
		Short: "Hold an " + kind + " at the top of `slate next`, above every priority",
		Long: "A pin is the one way work crosses a priority. It stands until `unpin`, or\n" +
			"until the " + kind + " " + closes + ".\n" +
			"Whatever the pinned work waits on sorts just ahead of it. Pinned records sort\n" +
			"among themselves by priority, then rank. Only a person reaches this, so an\n" +
			"automated order never pins its own favorite.",
		Example:     example,
		Args:        cobra.ExactArgs(1),
		Annotations: personOnly(),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAPIClient(cmd)
			if err != nil {
				return handleAPIError(err)
			}
			place := agentPlace(session)
			written, effects, pinned, err := queuePlaceWrites[kind].pin(cmd.Context(), client, args[0], api.PinInput{Session: place.Session, Host: place.Host})
			if err != nil {
				return handleArgumentAPIError(err)
			}
			return reportWrite(cmd, asJSON, written, effects, fmt.Sprintf("Pinned %s", pinned))
		},
	}
	addActingSessionFlag(cmd.Flags(), &session, agentSessionHelp)
	cmd.Flags().BoolVar(&asJSON, "json", false, writtenJSONHelp)
	return cmd
}

// newUnpinCommand unpins a record of one kind.
func newUnpinCommand(kind, example string) *cobra.Command {
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
			written, effects, unpinned, err := queuePlaceWrites[kind].unpin(cmd.Context(), client, args[0])
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
	return newPinCommand("issue", "closes", "  slate issues pin 412")
}

func newIssuesUnpinCommand() *cobra.Command {
	return newUnpinCommand("issue", "  slate issues unpin 412")
}

func newInitiativesPinCommand() *cobra.Command {
	return newPinCommand("initiative", "finishes or is shelved", "  slate initiatives pin \"Ship the tracker\"")
}

func newInitiativesUnpinCommand() *cobra.Command {
	return newUnpinCommand("initiative", "  slate initiatives unpin \"Ship the tracker\"")
}

func newIdeasPinCommand() *cobra.Command {
	return newPinCommand("idea", "is promoted, which moves the pin to its initiative", "  slate ideas pin 1530")
}

func newIdeasUnpinCommand() *cobra.Command {
	return newUnpinCommand("idea", "  slate ideas unpin 1530")
}

// withPin is a detail view's priority, followed by the pin that holds the record ahead of it.
func withPin(priority string, pinnedTS *time.Time, pinnedBy *string) string {
	if pinnedTS == nil {
		return priority
	}
	return fmt.Sprintf("%s — pinned ahead of every priority by %s on %s", priority, orDash(strValue(pinnedBy)), localDay(*pinnedTS))
}
