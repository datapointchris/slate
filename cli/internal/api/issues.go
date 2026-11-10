package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// The issue statuses, in lifecycle order. `triage` holds what a machine filed
// until someone accepts it. Whether anyone works an open issue is its claim.
const (
	IssueStatusTriage    = "triage"
	IssueStatusOpen      = "open"
	IssueStatusCompleted = "completed"
	IssueStatusCanceled  = "canceled"
	IssueStatusAll       = "all"
)

// IssueStatuses is what --status accepts, in the order help lists them.
var IssueStatuses = []string{
	IssueStatusTriage, IssueStatusOpen, IssueStatusCompleted, IssueStatusCanceled, IssueStatusAll,
}

// IssueTypeDecision is the one type that changes what happens to an issue: it
// waits on a person, so the queue lists it at step decide, and holding the
// queue's head leaves it out unless asked for.
const IssueTypeDecision = "decision"

// IssueTypes is what --type accepts and the guided form offers. It matches the
// API's issue_types table, so a type added there is added here too.
var IssueTypes = []string{"bug", "feature", "task", "chore", IssueTypeDecision}

// IssuePriorityNames is Linear's scale, indexed by value. 0 is the absence of a
// priority and sorts after every level.
var IssuePriorityNames = []string{"none", "urgent", "high", "medium", "low"}

// IssueSummary is enough of another issue to print it beside this one.
type IssueSummary struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// InitiativeSummary is the initiative an issue belongs to, with the priority it
// lends to issues that have none of their own.
type InitiativeSummary struct {
	ID       string `json:"id"`
	Number   int    `json:"number"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Priority int    `json:"priority"`
	Autofix  bool   `json:"autofix"`
}

// Issue mirrors the API's issue read. Every list row carries what a table or an
// agent needs — the edges as summaries, readiness, counts — so nothing fans out
// a request per row.
type Issue struct {
	ID           string  `json:"id"`
	Number       int     `json:"number"`
	Title        string  `json:"title"`
	Description  *string `json:"description"`
	Acceptance   *string `json:"acceptance"`
	Repo         *string `json:"repo"`
	Type         string  `json:"type"`
	Status       string  `json:"status"`
	StatusReason *string `json:"status_reason"`
	Priority     int     `json:"priority"`
	// EffectivePriority is what the ready queue sorts by: the issue's own, else
	// its parent's or its active initiative's, raised to the most urgent of
	// anything it blocks.
	EffectivePriority int        `json:"effective_priority"`
	Rank              float64    `json:"rank"`
	PinnedTS          *time.Time `json:"pinned_ts"`
	PinnedBy          *string    `json:"pinned_by"`
	PinnedSession     *string    `json:"pinned_session"`
	// DeferredUntilDate is a day, so it decodes as the string the API sends. A
	// time.Time would refuse the bare YYYY-MM-DD and fail the whole list.
	DeferredUntilDate *string `json:"deferred_until_date"`
	// Claim is the issue's unended claim, live or expired, and nil when nobody
	// holds it.
	Claim *Claim `json:"claim"`
	// Autofix is the issue's own setting. IsAutofixable is false when it, its
	// initiative's or an unclosed parent's is false, and nil from an API that
	// predates the field.
	Autofix        bool               `json:"autofix"`
	Initiative     *InitiativeSummary `json:"initiative"`
	Parent         *IssueSummary      `json:"parent"`
	DiscoveredFrom *IssueSummary      `json:"discovered_from"`
	DuplicateOf    *IssueSummary      `json:"duplicate_of"`
	Labels         []string           `json:"labels"`
	// Tags are the issue's own, which a write sets. EffectiveTags are its own
	// plus its initiative's, which a --tag filter matches.
	Tags          []string `json:"tags"`
	EffectiveTags []string `json:"effective_tags"`
	// Pursuits are its initiative's, since an issue joins none itself.
	Pursuits []PursuitSummary `json:"pursuits"`
	// Directions is Pursuits under the name the release before pursuits
	// served, so a reader of that name keeps reading it until a later release
	// drops it.
	Directions []PursuitSummary `json:"directions"`
	// Paths are the files and directories the issue changes, in its repo. A
	// directory covers everything under it.
	Paths     []string       `json:"paths"`
	DependsOn []IssueSummary `json:"depends_on"`
	Blocks    []IssueSummary `json:"blocks"`
	// InitiativeWaitsOn is each record an unclosed issue waits on through its
	// initiative, open or closed. WaitingInitiatives is each initiative waiting
	// on this issue. BlockedBy is each open record holding it back now, and is
	// empty exactly when IsBlocked is false.
	InitiativeWaitsOn  []RecordSummary `json:"initiative_waits_on"`
	WaitingInitiatives []RecordSummary `json:"waiting_initiatives"`
	BlockedBy          []Blocker       `json:"blocked_by"`
	ChildCount         int             `json:"child_count"`
	OpenChildCount     int             `json:"open_child_count"`
	CommentCount       int             `json:"comment_count"`
	IsBlocked          bool            `json:"is_blocked"`
	IsReady            bool            `json:"is_ready"`
	IsAutofixable      *bool           `json:"is_autofixable"`
	CreatedTS          time.Time       `json:"created_ts"`
	UpdatedTS          time.Time       `json:"updated_ts"`
	ClosedTS           *time.Time      `json:"closed_ts"`
	ClosedBy           *string         `json:"closed_by"`
}

// IssueDetail is one issue with its children, claims, comments and documents.
type IssueDetail struct {
	Issue
	RecordParts
	Children []IssueSummary `json:"children"`
}

// IssueCreateInput is the body for POST /issues. Only Title is required. The
// references take a number or a UUID for an issue, and a name or a UUID for an
// initiative; the API resolves either.
type IssueCreateInput struct {
	Title             string   `json:"title"`
	Description       *string  `json:"description,omitempty"`
	Acceptance        *string  `json:"acceptance,omitempty"`
	Repo              *string  `json:"repo,omitempty"`
	Type              *string  `json:"type,omitempty"`
	Status            *string  `json:"status,omitempty"`
	Priority          *int     `json:"priority,omitempty"`
	DeferredUntilDate *string  `json:"deferred_until_date,omitempty"`
	Autofix           *bool    `json:"autofix,omitempty"`
	Initiative        *string  `json:"initiative,omitempty"`
	Parent            *string  `json:"parent,omitempty"`
	DiscoveredFrom    *string  `json:"discovered_from,omitempty"`
	Labels            []string `json:"labels,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	DependsOn         []string `json:"depends_on,omitempty"`
	Paths             []string `json:"paths,omitempty"`
}

// IssueUpdateInput is a partial update. A field left nil is not sent. A field
// to empty goes in the clear list UpdateIssue takes, which sends it as an
// explicit null. Status changes through here too, and the server stamps
// closed_ts and ends the claim as the new status requires. ClosedBy names who
// is closing it, and only goes with a closed status; left nil, the server
// records the signed-in user.
type IssueUpdateInput struct {
	Title             *string   `json:"title,omitempty"`
	Description       *string   `json:"description,omitempty"`
	Acceptance        *string   `json:"acceptance,omitempty"`
	Repo              *string   `json:"repo,omitempty"`
	Type              *string   `json:"type,omitempty"`
	Status            *string   `json:"status,omitempty"`
	StatusReason      *string   `json:"status_reason,omitempty"`
	Priority          *int      `json:"priority,omitempty"`
	DeferredUntilDate *string   `json:"deferred_until_date,omitempty"`
	Autofix           *bool     `json:"autofix,omitempty"`
	Initiative        *string   `json:"initiative,omitempty"`
	Parent            *string   `json:"parent,omitempty"`
	DiscoveredFrom    *string   `json:"discovered_from,omitempty"`
	DuplicateOf       *string   `json:"duplicate_of,omitempty"`
	ClosedBy          *string   `json:"closed_by,omitempty"`
	Labels            *[]string `json:"labels,omitempty"`
	Paths             *[]string `json:"paths,omitempty"`
	Tags              *[]string `json:"tags,omitempty"`
}

// IssueFilter narrows a list. A zero field narrows nothing. Repo is a pointer
// because an empty string is its own question: the issues no single repo owns.
// A pointer field's zero value is an answer too, so nil is what narrows nothing.
type IssueFilter struct {
	Status      string
	Repo        *string
	Type        string
	Label       string
	Tag         string
	Pursuit     string
	Initiative  string
	Parent      string
	Priority    *int
	Blocked     *bool
	Autofixable *bool
	ClosedBy    string
	Search      string
}

func (f IssueFilter) query() url.Values {
	query := repoQuery(f.Repo)
	for name, value := range map[string]string{
		"status":     f.Status,
		"type":       f.Type,
		"label":      f.Label,
		"tag":        f.Tag,
		"pursuit":    f.Pursuit,
		"initiative": f.Initiative,
		"parent":     f.Parent,
		"closed_by":  f.ClosedBy,
		"search":     f.Search,
	} {
		if value != "" {
			query.Set(name, value)
		}
	}
	if f.Priority != nil {
		query.Set("priority", strconv.Itoa(*f.Priority))
	}
	if f.Blocked != nil {
		query.Set("blocked", strconv.FormatBool(*f.Blocked))
	}
	if f.Autofixable != nil {
		query.Set("autofixable", strconv.FormatBool(*f.Autofixable))
	}
	return query
}

// ReadyFilter narrows the ready issues. HoldNextClaim takes from them at step
// build, which refuses Type decision, and QueueFilter carries it beside its step.
type ReadyFilter struct {
	Repo        *string `json:"repo,omitempty"`
	Type        string  `json:"type,omitempty"`
	Label       string  `json:"label,omitempty"`
	Tag         string  `json:"tag,omitempty"`
	Pursuit     string  `json:"pursuit,omitempty"`
	Initiative  string  `json:"initiative,omitempty"`
	Autofixable *bool   `json:"autofixable,omitempty"`
}

func (f ReadyFilter) query() url.Values {
	query := repoQuery(f.Repo)
	for name, value := range map[string]string{
		"type": f.Type, "label": f.Label, "tag": f.Tag, "pursuit": f.Pursuit, "initiative": f.Initiative,
	} {
		if value != "" {
			query.Set(name, value)
		}
	}
	if f.Autofixable != nil {
		query.Set("autofixable", strconv.FormatBool(*f.Autofixable))
	}
	return query
}

// RankMove places a record directly before or after another record, of any
// kind. Exactly one of the two is set.
type RankMove struct {
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// Every read that returns an issue takes a DayZone. Readiness turns on whether
// a deferral day has arrived, and the zone names the calendar that answers it.
// An empty zone leaves the server to read it in the user's preference.

func issuePath(ref string, rest ...string) string {
	path := "/issues/" + url.PathEscape(ref)
	for _, segment := range rest {
		path += "/" + url.PathEscape(segment)
	}
	return path
}

func zoneQuery(zone DayZone) url.Values {
	query := url.Values{}
	setZone(query, zone)
	return query
}

func setZone(query url.Values, zone DayZone) {
	if zone != "" {
		query.Set("timezone", string(zone))
	}
}

// ListIssues returns issues in queue order: unclosed first by effective
// priority and rank, then closed ones latest first. An empty Status takes the
// API's default, every issue not yet completed or canceled. The window
// narrows on when an issue closed.
func (c *Client) ListIssues(ctx context.Context, filter IssueFilter, window Window, zone DayZone, limit *int) ([]Issue, error) {
	query := filter.query()
	applyWindow(query, window)
	setZone(query, zone)
	applyLimit(query, limit)
	var issues []Issue
	if err := c.get(ctx, withQuery("/issues", query), &issues); err != nil {
		return nil, err
	}
	return issues, nil
}

// GetIssue returns one issue by number or UUID, with its children, comments
// and documents.
func (c *Client) GetIssue(ctx context.Context, ref string, zone DayZone) (IssueDetail, error) {
	var issue IssueDetail
	if err := c.get(ctx, withQuery(issuePath(ref), zoneQuery(zone)), &issue); err != nil {
		return IssueDetail{}, err
	}
	return issue, nil
}

// CreateIssue files an issue and returns it.
func (c *Client) CreateIssue(ctx context.Context, in IssueCreateInput, zone DayZone) (IssueWritten, error) {
	var written IssueWritten
	if err := c.send(ctx, http.MethodPost, withQuery("/issues", zoneQuery(zone)), in, &written); err != nil {
		return IssueWritten{}, err
	}
	return written, nil
}

// UpdateIssue applies a partial update. Each field named in clear (API JSON
// keys) is sent as an explicit null, which is the only way to empty it.
func (c *Client) UpdateIssue(ctx context.Context, ref string, in IssueUpdateInput, clear []string, zone DayZone) (IssueWritten, error) {
	body, err := mergeClearNulls(in, clear)
	if err != nil {
		return IssueWritten{}, err
	}
	var written IssueWritten
	if err := c.send(ctx, http.MethodPatch, withQuery(issuePath(ref), zoneQuery(zone)), body, &written); err != nil {
		return IssueWritten{}, err
	}
	return written, nil
}

// DeleteIssue removes an issue, its edges, claims, worklogs, documents and
// comments. The answer names what it took and each issue that lost its link to
// it, giving each time in zone.
func (c *Client) DeleteIssue(ctx context.Context, ref string, zone DayZone) (RecordDeleted, error) {
	var deleted RecordDeleted
	if err := c.send(ctx, http.MethodDelete, withQuery(issuePath(ref), zoneQuery(zone)), nil, &deleted); err != nil {
		return RecordDeleted{}, err
	}
	return deleted, nil
}

// RankIssue places an issue directly before or after another record.
func (c *Client) RankIssue(ctx context.Context, ref string, move RankMove, zone DayZone) (IssueWritten, error) {
	var written IssueWritten
	if err := c.send(ctx, http.MethodPost, withQuery(issuePath(ref, "rank"), zoneQuery(zone)), move, &written); err != nil {
		return IssueWritten{}, err
	}
	return written, nil
}
