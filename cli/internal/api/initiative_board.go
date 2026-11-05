package api

import (
	"context"
	"net/url"
	"time"
)

// The states a board issue's State holds, beside IssueStatusCompleted and
// IssueStatusCanceled.
const (
	BoardStateRunning      = "running"
	BoardStateWaiting      = "waiting"
	BoardStateBlocked      = "blocked"
	BoardStateOpenChildren = "open_children"
)

// BoardCloses is the issues completed inside each window, ending at the read. A
// canceled issue never counts, and ByDay holds one count per day of the board's Days.
type BoardCloses struct {
	Last24h      int        `json:"last_24h"`
	Last7d       int        `json:"last_7d"`
	Last30d      int        `json:"last_30d"`
	LastClosedTS *time.Time `json:"last_closed_ts"`
	ByDay        []int      `json:"by_day"`
}

// BoardCounts is a row's issues. States partitions IssueCount, and OpenCount is
// those neither completed nor canceled.
type BoardCounts struct {
	IssueCount int              `json:"issue_count"`
	OpenCount  int              `json:"open_count"`
	States     IssueStateCounts `json:"states"`
	Closes     BoardCloses      `json:"closes"`
}

// BoardRollup is the counts and closes over a set of initiatives.
type BoardRollup struct {
	BoardCounts
	InitiativeCount int `json:"initiative_count"`
}

// BoardTier is the rollup of the initiatives at one priority.
type BoardTier struct {
	BoardRollup
	Priority int `json:"priority"`
}

// BoardIssue is one issue on the board.
type BoardIssue struct {
	Number            int        `json:"number"`
	Title             string     `json:"title"`
	State             string     `json:"state"`
	Type              string     `json:"type"`
	Priority          int        `json:"priority"`
	EffectivePriority int        `json:"effective_priority"`
	Repo              *string    `json:"repo"`
	Holder            *string    `json:"holder"`
	ClosedTS          *time.Time `json:"closed_ts"`
}

// BoardInitiative is one initiative on the board. Issues holds every issue in
// it: the unclosed in queue order, then the closed, latest first.
type BoardInitiative struct {
	BoardCounts
	Number    int          `json:"number"`
	Name      string       `json:"name"`
	Status    string       `json:"status"`
	Priority  int          `json:"priority"`
	Repos     []string     `json:"repos"`
	PinnedTS  *time.Time   `json:"pinned_ts"`
	PinnedBy  *string      `json:"pinned_by"`
	Claim     *Claim       `json:"claim"`
	CreatedTS time.Time    `json:"created_ts"`
	ClosedTS  *time.Time   `json:"closed_ts"`
	Issues    []BoardIssue `json:"issues"`
}

// InitiativeBoard is the board as of ReadTS. Days are the calendar days each
// ByDay counts, oldest first, in Zone. Tiers holds every priority, whether or
// not an initiative is at it, and Initiatives comes in list order.
type InitiativeBoard struct {
	ReadTS      time.Time         `json:"read_ts"`
	Zone        string            `json:"zone"`
	Days        []string          `json:"days"`
	Totals      BoardRollup       `json:"totals"`
	Tiers       []BoardTier       `json:"tiers"`
	Initiatives []BoardInitiative `json:"initiatives"`
}

// BoardIssueText is an issue's first paragraph of prose, nil where it has none.
type BoardIssueText struct {
	Number  int     `json:"number"`
	Excerpt *string `json:"excerpt"`
}

// BoardInitiativeText is an initiative's long text.
type BoardInitiativeText struct {
	Number       int              `json:"number"`
	Summary      string           `json:"summary"`
	Description  *string          `json:"description"`
	StatusReason *string          `json:"status_reason"`
	Issues       []BoardIssueText `json:"issues"`
}

// InitiativeBoardText is the long text of the initiatives the board lists.
type InitiativeBoardText struct {
	ReadTS      time.Time             `json:"read_ts"`
	Initiatives []BoardInitiativeText `json:"initiatives"`
}

func boardQuery(initiativeStatus string) url.Values {
	query := url.Values{}
	if initiativeStatus != "" {
		query.Set("status", initiativeStatus)
	}
	return query
}

// ReadInitiativeBoard reads every initiative at a status, with its counts,
// closes and slim issue rows. An empty status takes the API's default, the
// active ones, and zone is the calendar the days are counted in.
func (c *Client) ReadInitiativeBoard(ctx context.Context, initiativeStatus string, zone DayZone) (InitiativeBoard, error) {
	query := boardQuery(initiativeStatus)
	for name, values := range zoneQuery(zone) {
		query[name] = values
	}
	var board InitiativeBoard
	if err := c.get(ctx, withQuery("/initiatives/board", query), &board); err != nil {
		return InitiativeBoard{}, err
	}
	return board, nil
}

// ReadInitiativeBoardText reads the long text of the initiatives the board
// lists at a status, apart so a board's poll never carries it.
func (c *Client) ReadInitiativeBoardText(ctx context.Context, initiativeStatus string) (InitiativeBoardText, error) {
	var text InitiativeBoardText
	if err := c.get(ctx, withQuery("/initiatives/board/text", boardQuery(initiativeStatus)), &text); err != nil {
		return InitiativeBoardText{}, err
	}
	return text, nil
}
