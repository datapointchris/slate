package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// InitiativeStatuses is what an initiative's --status accepts.
var InitiativeStatuses = []string{"active", "someday", "completed", "dropped", "all"}

// InitiativeSorts is what an initiative list's --sort accepts. priority is the
// order to work them in, and the API's default.
var InitiativeSorts = []string{"priority", "open", "newest"}

// InitiativeStatusesClosed are the statuses whose initiative the API refuses
// to file a new issue into until it is reopened.
var InitiativeStatusesClosed = []string{"completed", InitiativeStatusDropped}

// InitiativeStatusDropped is an initiative given up on, which a tree marks as
// it marks a canceled issue.
const InitiativeStatusDropped = "dropped"

// Initiative is a bounded outcome a set of issues ships together. The counts
// cover every issue in it. Repos names the repos its issues touch, leaving out
// canceled issues and keeping completed ones. DependsOn is each initiative or
// issue it waits on, and WaitingInitiatives each initiative waiting on it,
// open or closed. Steps is each step checked on it in order, and Stage the
// furthest of them, or idea before any.
type Initiative struct {
	ID            string     `json:"id"`
	Number        int        `json:"number"`
	Name          string     `json:"name"`
	Summary       string     `json:"summary"`
	Description   *string    `json:"description"`
	Status        string     `json:"status"`
	StatusReason  *string    `json:"status_reason"`
	Priority      int        `json:"priority"`
	Rank          float64    `json:"rank"`
	PinnedTS      *time.Time `json:"pinned_ts"`
	PinnedBy      *string    `json:"pinned_by"`
	PinnedSession *string    `json:"pinned_session"`
	Autofix       bool       `json:"autofix"`
	CreatedTS     time.Time  `json:"created_ts"`
	ClosedTS      *time.Time `json:"closed_ts"`
	Tags          []string   `json:"tags"`
	// Directions is each direction it is in, in number order. Each of its
	// issues reads them as its own.
	Directions         []DirectionSummary `json:"directions"`
	IssueCount         int                `json:"issue_count"`
	OpenCount          int                `json:"open_count"`
	CompletedCount     int                `json:"completed_count"`
	CanceledCount      int                `json:"canceled_count"`
	Repos              []string           `json:"repos"`
	Claim              *Claim             `json:"claim"`
	Idea               *RecordSummary     `json:"idea"`
	DependsOn          []RecordSummary    `json:"depends_on"`
	WaitingInitiatives []RecordSummary    `json:"waiting_initiatives"`
	Steps              []CheckedStep      `json:"steps"`
	Stage              string             `json:"stage"`
}

// InitiativeDetail is an initiative with its claims, comments and documents.
type InitiativeDetail struct {
	Initiative
	RecordParts
}

// InitiativeCreateInput is the body for POST /initiatives. The initiative ranks
// last, and RankInitiative moves it.
type InitiativeCreateInput struct {
	Name        string   `json:"name"`
	Summary     string   `json:"summary"`
	Description *string  `json:"description,omitempty"`
	Priority    *int     `json:"priority,omitempty"`
	Autofix     *bool    `json:"autofix,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Directions  []int    `json:"directions,omitempty"`
}

// InitiativeUpdateInput is a partial update, cleared the way IssueUpdateInput
// is. Tags and Directions each replace the whole set, and every issue in it
// reads the new set.
type InitiativeUpdateInput struct {
	Name         *string   `json:"name,omitempty"`
	Summary      *string   `json:"summary,omitempty"`
	Description  *string   `json:"description,omitempty"`
	Status       *string   `json:"status,omitempty"`
	StatusReason *string   `json:"status_reason,omitempty"`
	Priority     *int      `json:"priority,omitempty"`
	Autofix      *bool     `json:"autofix,omitempty"`
	Tags         *[]string `json:"tags,omitempty"`
	Directions   *[]int    `json:"directions,omitempty"`
}

func initiativePath(ref string) string {
	return "/initiatives/" + url.PathEscape(ref)
}

// InitiativeFilter narrows a list of initiatives. A zero field is left off the
// query, so the API's own default applies.
type InitiativeFilter struct {
	Status    string
	Sort      string
	Tag       string
	Direction string
}

// ListInitiatives returns initiatives in the order to work them.
func (c *Client) ListInitiatives(ctx context.Context, filter InitiativeFilter, limit *int) ([]Initiative, error) {
	query := url.Values{}
	for name, value := range map[string]string{
		"status": filter.Status, "sort": filter.Sort, "tag": filter.Tag, "direction": filter.Direction,
	} {
		if value != "" {
			query.Set(name, value)
		}
	}
	applyLimit(query, limit)
	var initiatives []Initiative
	if err := c.get(ctx, withQuery("/initiatives", query), &initiatives); err != nil {
		return nil, err
	}
	return initiatives, nil
}

// GetInitiative returns one initiative by any reference the API resolves, with
// its claim history.
func (c *Client) GetInitiative(ctx context.Context, ref string) (InitiativeDetail, error) {
	var initiative InitiativeDetail
	if err := c.get(ctx, initiativePath(ref), &initiative); err != nil {
		return InitiativeDetail{}, err
	}
	return initiative, nil
}

// CreateInitiative starts an initiative.
func (c *Client) CreateInitiative(ctx context.Context, in InitiativeCreateInput) (InitiativeWritten, error) {
	var written InitiativeWritten
	if err := c.send(ctx, http.MethodPost, "/initiatives", in, &written); err != nil {
		return InitiativeWritten{}, err
	}
	return written, nil
}

// UpdateInitiative applies a partial update, sending each field in clear as
// null. The answer's messages give each time in zone.
func (c *Client) UpdateInitiative(ctx context.Context, ref string, in InitiativeUpdateInput, clear []string, zone DayZone) (InitiativeWritten, error) {
	body, err := mergeClearNulls(in, clear)
	if err != nil {
		return InitiativeWritten{}, err
	}
	var written InitiativeWritten
	if err := c.send(ctx, http.MethodPatch, withQuery(initiativePath(ref), zoneQuery(zone)), body, &written); err != nil {
		return InitiativeWritten{}, err
	}
	return written, nil
}

// DeleteInitiative removes an initiative and its claims. Its issues stay,
// belonging to none, and the answer names each of them, giving each time in
// zone.
func (c *Client) DeleteInitiative(ctx context.Context, ref string, zone DayZone) (RecordDeleted, error) {
	var deleted RecordDeleted
	if err := c.send(ctx, http.MethodDelete, withQuery(initiativePath(ref), zoneQuery(zone)), nil, &deleted); err != nil {
		return RecordDeleted{}, err
	}
	return deleted, nil
}

// IssueStateCounts is how many of an initiative's issues are in each state the
// API derives. The counts partition its issue count. Running and Waiting are
// issues under a live claim, Waiting where the holder parked it. OpenChildren
// is an open issue nothing else holds back but its open children.
type IssueStateCounts struct {
	Completed    int `json:"completed"`
	Canceled     int `json:"canceled"`
	Running      int `json:"running"`
	Waiting      int `json:"waiting"`
	Ready        int `json:"ready"`
	Blocked      int `json:"blocked"`
	Triage       int `json:"triage"`
	Deferred     int `json:"deferred"`
	Shelved      int `json:"shelved"`
	OpenChildren int `json:"open_children"`
}

// InitiativeProgress is everything a progress view prints for one initiative.
// Running, Waiting and Blocked hold the issues in that state in queue order,
// Next is the head of the queue for the initiative, and Closed is every close
// between ClosedSince and ClosedUntil, latest first, where a nil bound leaves
// that end open. Totals cover the initiative's whole life.
type InitiativeProgress struct {
	Initiative  Initiative       `json:"initiative"`
	States      IssueStateCounts `json:"states"`
	Running     []Issue          `json:"running"`
	Waiting     []Issue          `json:"waiting"`
	Blocked     []Issue          `json:"blocked"`
	Next        []QueueRow       `json:"next"`
	Closed      []Issue          `json:"closed"`
	ClosedSince *time.Time       `json:"closed_since"`
	ClosedUntil *time.Time       `json:"closed_until"`
	Totals      WorkTotals       `json:"totals"`
	// Burnup is nil where the API answered none, as one older than this CLI does.
	Burnup *[]BurnupStep `json:"burnup"`
}

// BurnupStep is an initiative's issues from TS until the next step: Scope held in
// it and not canceled, and Completed of those. A progress read's Burnup is oldest
// first, over the initiative's whole life, and ends at the read.
type BurnupStep struct {
	TS        time.Time `json:"ts"`
	Scope     int       `json:"scope"`
	Completed int       `json:"completed"`
}

// ListInitiativesProgress reads the progress of each initiative refs names, by
// any reference the API resolves, in the order named. With none named, the API
// answers every active initiative in the order to work them. The window narrows
// only the closes, and zone is the calendar readiness is read in.
func (c *Client) ListInitiativesProgress(ctx context.Context, refs []string, window Window, zone DayZone) ([]InitiativeProgress, error) {
	query := zoneQuery(zone)
	for _, ref := range refs {
		query.Add("initiative", ref)
	}
	applyWindow(query, window)
	var progress []InitiativeProgress
	if err := c.get(ctx, withQuery("/initiatives/progress", query), &progress); err != nil {
		return nil, err
	}
	return progress, nil
}

// RankInitiative places an initiative, with its issues, directly before or
// after another record of its priority.
func (c *Client) RankInitiative(ctx context.Context, ref string, move RankMove, zone DayZone) (InitiativeWritten, error) {
	var written InitiativeWritten
	if err := c.send(ctx, http.MethodPost, withQuery(initiativePath(ref)+"/rank", zoneQuery(zone)), move, &written); err != nil {
		return InitiativeWritten{}, err
	}
	return written, nil
}
