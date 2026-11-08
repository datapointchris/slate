package api

import (
	"context"
	"time"
)

// ReadyIssue is an issue ready to take, and when it was filed, which is how long
// it has waited.
type ReadyIssue struct {
	Number            int       `json:"number"`
	Title             string    `json:"title"`
	EffectivePriority int       `json:"effective_priority"`
	CreatedTS         time.Time `json:"created_ts"`
}

// RepoRollup is a set of issues' counts and closes, with ClosesByWeek holding
// one count per week of the board's Weeks.
type RepoRollup struct {
	BoardCounts
	ClosesByWeek []int `json:"closes_by_week"`
}

// BoardRepo is one repo's issues, or every issue on no repo where Repo is nil.
// Ready is its ready issues, the longest waiting first.
type BoardRepo struct {
	RepoRollup
	Repo  *string      `json:"repo"`
	Ready []ReadyIssue `json:"ready"`
}

// RepoBoard is every repo the issues name, as of ReadTS. Days and Weeks are the
// days ByDay counts and the Mondays ClosesByWeek counts from, oldest first, in
// Zone. Repos comes by name, then the issues on no repo.
type RepoBoard struct {
	ReadTS time.Time   `json:"read_ts"`
	Zone   string      `json:"zone"`
	Days   []string    `json:"days"`
	Weeks  []string    `json:"weeks"`
	Totals RepoRollup  `json:"totals"`
	Repos  []BoardRepo `json:"repos"`
}

// ReadRepoBoard reads every repo's counts, closes and ready issues, its calendar
// in zone.
func (c *Client) ReadRepoBoard(ctx context.Context, zone DayZone) (RepoBoard, error) {
	var board RepoBoard
	if err := c.get(ctx, withQuery("/repos/board", zoneQuery(zone)), &board); err != nil {
		return RepoBoard{}, err
	}
	return board, nil
}
