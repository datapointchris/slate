package cli

import (
	"context"
	"fmt"

	"github.com/datapointchris/slate/cli/internal/api"
)

// queuePlaceWrite is how one kind's record is pinned, unpinned and moved in the queue. Each
// answers what the API wrote, what it did beyond the request, and the record named for the
// line the write prints.
type queuePlaceWrite struct {
	pin   func(ctx context.Context, client *api.Client, ref string, pin api.PinInput) (any, api.WriteEffects, string, error)
	unpin func(ctx context.Context, client *api.Client, ref string) (any, api.WriteEffects, string, error)
	rank  func(ctx context.Context, client *api.Client, ref string, move api.RankMove) (any, api.WriteEffects, string, error)
}

// queuePlaceWrites is every kind a pin or a reorder reaches, read by the verbs and by
// `slate tui`'s keys alike, so a kind missing here is refused by both.
var queuePlaceWrites = map[string]queuePlaceWrite{
	"issue": {
		pin: func(ctx context.Context, client *api.Client, ref string, pin api.PinInput) (any, api.WriteEffects, string, error) {
			written, err := client.PinIssue(ctx, ref, pin, issueZone())
			return written, written.WriteEffects, fmt.Sprintf("#%d %s", written.Issue.Number, written.Issue.Title), err
		},
		unpin: func(ctx context.Context, client *api.Client, ref string) (any, api.WriteEffects, string, error) {
			written, err := client.UnpinIssue(ctx, ref, issueZone())
			return written, written.WriteEffects, fmt.Sprintf("#%d %s", written.Issue.Number, written.Issue.Title), err
		},
		rank: func(ctx context.Context, client *api.Client, ref string, move api.RankMove) (any, api.WriteEffects, string, error) {
			written, err := client.RankIssue(ctx, ref, move, issueZone())
			return written, written.WriteEffects, fmt.Sprintf("#%d %s", written.Issue.Number, written.Issue.Title), err
		},
	},
	"initiative": {
		pin: func(ctx context.Context, client *api.Client, ref string, pin api.PinInput) (any, api.WriteEffects, string, error) {
			written, err := client.PinInitiative(ctx, ref, pin)
			return written, written.WriteEffects, fmt.Sprintf("#%d %s", written.Initiative.Number, written.Initiative.Name), err
		},
		unpin: func(ctx context.Context, client *api.Client, ref string) (any, api.WriteEffects, string, error) {
			written, err := client.UnpinInitiative(ctx, ref)
			return written, written.WriteEffects, fmt.Sprintf("#%d %s", written.Initiative.Number, written.Initiative.Name), err
		},
		rank: func(ctx context.Context, client *api.Client, ref string, move api.RankMove) (any, api.WriteEffects, string, error) {
			written, err := client.RankInitiative(ctx, ref, move, issueZone())
			return written, written.WriteEffects, fmt.Sprintf("initiative #%d %q", written.Initiative.Number, written.Initiative.Name), err
		},
	},
	"idea": {
		pin: func(ctx context.Context, client *api.Client, ref string, pin api.PinInput) (any, api.WriteEffects, string, error) {
			written, err := client.PinIdea(ctx, ref, pin)
			return written, written.WriteEffects, fmt.Sprintf("#%d %s", written.Idea.Number, written.Idea.Title), err
		},
		unpin: func(ctx context.Context, client *api.Client, ref string) (any, api.WriteEffects, string, error) {
			written, err := client.UnpinIdea(ctx, ref)
			return written, written.WriteEffects, fmt.Sprintf("#%d %s", written.Idea.Number, written.Idea.Title), err
		},
		rank: func(ctx context.Context, client *api.Client, ref string, move api.RankMove) (any, api.WriteEffects, string, error) {
			written, err := client.RankIdea(ctx, ref, move, issueZone())
			return written, written.WriteEffects, fmt.Sprintf("idea #%d %s", written.Idea.Number, written.Idea.Title), err
		},
	},
}
