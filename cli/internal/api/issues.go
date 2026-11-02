package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// The issue statuses, in lifecycle order. `triage` holds what a machine filed
// until someone accepts it, and `in_progress` is what a claim sets.
const (
	IssueStatusTriage     = "triage"
	IssueStatusOpen       = "open"
	IssueStatusInProgress = "in_progress"
	IssueStatusCompleted  = "completed"
	IssueStatusCanceled   = "canceled"
	IssueStatusAll        = "all"
)

// IssueStatuses is what --status accepts, in the order help lists them.
var IssueStatuses = []string{
	IssueStatusTriage, IssueStatusOpen, IssueStatusInProgress,
	IssueStatusCompleted, IssueStatusCanceled, IssueStatusAll,
}

// IssueTypeDecision is the one type that changes what happens to an issue: it
// waits on a person, so the ready queue leaves it out unless asked for.
const IssueTypeDecision = "decision"

// IssueTypes is what --type accepts and the guided form offers. It matches the
// API's issue_types table, so a type added there is added here too.
var IssueTypes = []string{"bug", "feature", "task", "chore", IssueTypeDecision}

// IssuePriorityNames is Linear's scale, indexed by value. 0 is the absence of a
// priority and sorts after every level.
var IssuePriorityNames = []string{"none", "urgent", "high", "medium", "low"}

// InitiativeStatuses is what an initiative's --status accepts.
var InitiativeStatuses = []string{"active", "someday", "completed", "dropped", "all"}

// InitiativeSorts is what an initiative list's --sort accepts. priority is the
// order to work them in, and the API's default.
var InitiativeSorts = []string{"priority", "open", "newest"}

// InitiativeStatusesClosed are the statuses whose initiative the API refuses
// to file a new issue into until it is reopened.
var InitiativeStatusesClosed = []string{"completed", "dropped"}

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
	EffectivePriority int     `json:"effective_priority"`
	Rank              float64 `json:"rank"`
	// DeferredUntilDate is a day, so it decodes as the string the API sends. A
	// time.Time would refuse the bare YYYY-MM-DD and fail the whole list.
	DeferredUntilDate *string    `json:"deferred_until_date"`
	ClaimedBy         *string    `json:"claimed_by"`
	ClaimExpiresTS    *time.Time `json:"claim_expires_ts"`
	// Autofix is the issue's own setting. IsAutofixable is false when it, its
	// initiative's or an unclosed parent's is false, and nil from an API that
	// predates the field.
	Autofix        bool               `json:"autofix"`
	Initiative     *InitiativeSummary `json:"initiative"`
	Parent         *IssueSummary      `json:"parent"`
	DiscoveredFrom *IssueSummary      `json:"discovered_from"`
	DuplicateOf    *IssueSummary      `json:"duplicate_of"`
	Labels         []string           `json:"labels"`
	DependsOn      []IssueSummary     `json:"depends_on"`
	Blocks         []IssueSummary     `json:"blocks"`
	ChildCount     int                `json:"child_count"`
	OpenChildCount int                `json:"open_child_count"`
	CommentCount   int                `json:"comment_count"`
	IsBlocked      bool               `json:"is_blocked"`
	IsReady        bool               `json:"is_ready"`
	IsAutofixable  *bool              `json:"is_autofixable"`
	CreatedTS      time.Time          `json:"created_ts"`
	UpdatedTS      time.Time          `json:"updated_ts"`
	ClosedTS       *time.Time         `json:"closed_ts"`
	ClosedBy       *string            `json:"closed_by"`
}

// IssueComment is a note on an issue: progress, a handoff, or what closing it shipped.
type IssueComment struct {
	ID        string    `json:"id"`
	IssueID   string    `json:"issue_id"`
	Body      string    `json:"body"`
	Author    *string   `json:"author"`
	CreatedTS time.Time `json:"created_ts"`
}

// IssueDetail is one issue with its children and its whole comment thread.
type IssueDetail struct {
	Issue
	Children []IssueSummary `json:"children"`
	Comments []IssueComment `json:"comments"`
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
	DependsOn         []string `json:"depends_on,omitempty"`
}

// IssueUpdateInput is a partial update. A field left nil is not sent. A field
// to empty goes in the clear list UpdateIssue takes, which sends it as an
// explicit null. Status changes through here too, and the server stamps
// closed_ts and drops the claim as the new status requires. ClosedBy names who
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
}

// IssueFilter narrows a list. A zero field narrows nothing. Repo is a pointer
// because an empty string is its own question: the issues no single repo owns.
// A pointer field's zero value is an answer too, so nil is what narrows nothing.
type IssueFilter struct {
	Status      string
	Repo        *string
	Type        string
	Label       string
	Initiative  string
	Parent      string
	Priority    *int
	Blocked     *bool
	Autofixable *bool
	ClaimedBy   string
	ClosedBy    string
	Search      string
}

func (f IssueFilter) query() url.Values {
	query := repoQuery(f.Repo)
	for name, value := range map[string]string{
		"status":     f.Status,
		"type":       f.Type,
		"label":      f.Label,
		"initiative": f.Initiative,
		"parent":     f.Parent,
		"claimed_by": f.ClaimedBy,
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

// ReadyFilter narrows the ready queue. An empty Type leaves decisions out,
// because they wait on a person. Type "decision" is the only way to see them.
type ReadyFilter struct {
	Repo        *string `json:"repo,omitempty"`
	Type        string  `json:"type,omitempty"`
	Label       string  `json:"label,omitempty"`
	Initiative  string  `json:"initiative,omitempty"`
	Autofixable *bool   `json:"autofixable,omitempty"`
}

func (f ReadyFilter) query() url.Values {
	query := repoQuery(f.Repo)
	for name, value := range map[string]string{"type": f.Type, "label": f.Label, "initiative": f.Initiative} {
		if value != "" {
			query.Set(name, value)
		}
	}
	if f.Autofixable != nil {
		query.Set("autofixable", strconv.FormatBool(*f.Autofixable))
	}
	return query
}

// IssueClaimInput takes an issue for Minutes. A zero Minutes takes the API's
// default.
type IssueClaimInput struct {
	Claimant string `json:"claimant"`
	Minutes  int    `json:"minutes,omitempty"`
}

type readyClaimInput struct {
	IssueClaimInput
	ReadyFilter
}

// IssueClaimResult holds the claimed issue, or nil when nothing was ready.
type IssueClaimResult struct {
	Issue *Issue `json:"issue"`
}

// IssueRankMove places an issue directly before or after another. Exactly one
// of the two is set.
type IssueRankMove struct {
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

// ListReadyIssues returns the issues an agent could take now, in the order to
// take them.
func (c *Client) ListReadyIssues(ctx context.Context, filter ReadyFilter, zone DayZone, limit *int) ([]Issue, error) {
	query := filter.query()
	setZone(query, zone)
	applyLimit(query, limit)
	var issues []Issue
	if err := c.get(ctx, withQuery("/issues/ready", query), &issues); err != nil {
		return nil, err
	}
	return issues, nil
}

// ClaimNextIssue takes the head of the ready queue in one request, so two
// agents asking at once never hold the same issue.
func (c *Client) ClaimNextIssue(ctx context.Context, claim IssueClaimInput, filter ReadyFilter, zone DayZone) (IssueClaimResult, error) {
	var result IssueClaimResult
	path := withQuery("/issues/ready/claim", zoneQuery(zone))
	if err := c.send(ctx, http.MethodPost, path, readyClaimInput{claim, filter}, &result); err != nil {
		return IssueClaimResult{}, err
	}
	return result, nil
}

// GetIssue returns one issue by number or UUID, with its children and comments.
func (c *Client) GetIssue(ctx context.Context, ref string, zone DayZone) (IssueDetail, error) {
	var issue IssueDetail
	if err := c.get(ctx, withQuery(issuePath(ref), zoneQuery(zone)), &issue); err != nil {
		return IssueDetail{}, err
	}
	return issue, nil
}

// CreateIssue files an issue and returns it.
func (c *Client) CreateIssue(ctx context.Context, in IssueCreateInput, zone DayZone) (IssueDetail, error) {
	var issue IssueDetail
	if err := c.send(ctx, http.MethodPost, withQuery("/issues", zoneQuery(zone)), in, &issue); err != nil {
		return IssueDetail{}, err
	}
	return issue, nil
}

// UpdateIssue applies a partial update. Each field named in clear (API JSON
// keys) is sent as an explicit null, which is the only way to empty it.
func (c *Client) UpdateIssue(ctx context.Context, ref string, in IssueUpdateInput, clear []string, zone DayZone) (IssueDetail, error) {
	body, err := mergeClearNulls(in, clear)
	if err != nil {
		return IssueDetail{}, err
	}
	var issue IssueDetail
	if err := c.send(ctx, http.MethodPatch, withQuery(issuePath(ref), zoneQuery(zone)), body, &issue); err != nil {
		return IssueDetail{}, err
	}
	return issue, nil
}

// DeleteIssue removes an issue, its comments and its edges.
func (c *Client) DeleteIssue(ctx context.Context, ref string) error {
	return c.send(ctx, http.MethodDelete, issuePath(ref), nil, nil)
}

// ClaimIssue takes one named issue, or extends the claim held under the same name.
func (c *Client) ClaimIssue(ctx context.Context, ref string, claim IssueClaimInput, zone DayZone) (Issue, error) {
	var issue Issue
	if err := c.send(ctx, http.MethodPost, withQuery(issuePath(ref, "claim"), zoneQuery(zone)), claim, &issue); err != nil {
		return Issue{}, err
	}
	return issue, nil
}

// ReleaseIssue returns an in-progress issue to the ready queue.
func (c *Client) ReleaseIssue(ctx context.Context, ref string, zone DayZone) (Issue, error) {
	var issue Issue
	if err := c.send(ctx, http.MethodDelete, withQuery(issuePath(ref, "claim"), zoneQuery(zone)), nil, &issue); err != nil {
		return Issue{}, err
	}
	return issue, nil
}

// RankIssue places an issue directly before or after another.
func (c *Client) RankIssue(ctx context.Context, ref string, move IssueRankMove, zone DayZone) (Issue, error) {
	var issue Issue
	if err := c.send(ctx, http.MethodPost, withQuery(issuePath(ref, "rank"), zoneQuery(zone)), move, &issue); err != nil {
		return Issue{}, err
	}
	return issue, nil
}

// AddIssueDependency records that ref waits on dependsOn. The API refuses an
// edge that would leave an issue waiting on itself.
func (c *Client) AddIssueDependency(ctx context.Context, ref, dependsOn string, zone DayZone) (IssueDetail, error) {
	var issue IssueDetail
	body := map[string]string{"depends_on": dependsOn}
	if err := c.send(ctx, http.MethodPost, withQuery(issuePath(ref, "dependencies"), zoneQuery(zone)), body, &issue); err != nil {
		return IssueDetail{}, err
	}
	return issue, nil
}

// RemoveIssueDependency removes the edge from ref to dependsOn.
func (c *Client) RemoveIssueDependency(ctx context.Context, ref, dependsOn string) error {
	return c.send(ctx, http.MethodDelete, issuePath(ref, "dependencies", dependsOn), nil, nil)
}

// ListIssueComments returns an issue's comments, oldest first.
func (c *Client) ListIssueComments(ctx context.Context, ref string, limit *int) ([]IssueComment, error) {
	query := url.Values{}
	applyLimit(query, limit)
	var comments []IssueComment
	if err := c.get(ctx, withQuery(issuePath(ref, "comments"), query), &comments); err != nil {
		return nil, err
	}
	return comments, nil
}

// AddIssueComment leaves a comment on an issue. A nil author leaves it unsigned.
func (c *Client) AddIssueComment(ctx context.Context, ref, body string, author *string) (IssueComment, error) {
	in := struct {
		Body   string  `json:"body"`
		Author *string `json:"author,omitempty"`
	}{body, author}
	var comment IssueComment
	if err := c.send(ctx, http.MethodPost, issuePath(ref, "comments"), in, &comment); err != nil {
		return IssueComment{}, err
	}
	return comment, nil
}

// RemoveIssueComment deletes one comment from an issue.
func (c *Client) RemoveIssueComment(ctx context.Context, ref, commentID string) error {
	return c.send(ctx, http.MethodDelete, issuePath(ref, "comments", commentID), nil, nil)
}

// IssuePriority names one value of the priority scale.
type IssuePriority struct {
	Value int    `json:"value"`
	Name  string `json:"name"`
}

// IssueVocabulary is every closed value an issue field takes, in the order a
// picker lists them. Labels come with it because they are the one vocabulary
// that grows at runtime.
type IssueVocabulary struct {
	Statuses           []string        `json:"statuses"`
	Types              []string        `json:"types"`
	Priorities         []IssuePriority `json:"priorities"`
	InitiativeStatuses []string        `json:"initiative_statuses"`
	Labels             []IssueLabel    `json:"labels"`
}

// GetIssueVocabulary returns the values each closed issue field accepts.
func (c *Client) GetIssueVocabulary(ctx context.Context) (IssueVocabulary, error) {
	var vocabulary IssueVocabulary
	if err := c.get(ctx, "/issues/vocabulary", &vocabulary); err != nil {
		return IssueVocabulary{}, err
	}
	return vocabulary, nil
}

// Initiative is a bounded outcome a set of issues ships together. The counts
// cover every issue in it. Repos names the repos its issues touch, leaving out
// canceled issues and keeping completed ones.
type Initiative struct {
	ID             string     `json:"id"`
	Number         int        `json:"number"`
	Name           string     `json:"name"`
	Description    *string    `json:"description"`
	Status         string     `json:"status"`
	StatusReason   *string    `json:"status_reason"`
	Priority       int        `json:"priority"`
	Position       int        `json:"position"`
	Autofix        bool       `json:"autofix"`
	CreatedTS      time.Time  `json:"created_ts"`
	ClosedTS       *time.Time `json:"closed_ts"`
	IssueCount     int        `json:"issue_count"`
	OpenCount      int        `json:"open_count"`
	CompletedCount int        `json:"completed_count"`
	CanceledCount  int        `json:"canceled_count"`
	Repos          []string   `json:"repos"`
}

// InitiativeCreateInput is the body for POST /issues/initiatives. A nil
// Position puts the initiative last.
type InitiativeCreateInput struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	Priority    *int    `json:"priority,omitempty"`
	Position    *int    `json:"position,omitempty"`
	Autofix     *bool   `json:"autofix,omitempty"`
}

// InitiativeUpdateInput is a partial update, cleared the way IssueUpdateInput is.
type InitiativeUpdateInput struct {
	Name         *string `json:"name,omitempty"`
	Description  *string `json:"description,omitempty"`
	Status       *string `json:"status,omitempty"`
	StatusReason *string `json:"status_reason,omitempty"`
	Priority     *int    `json:"priority,omitempty"`
	Position     *int    `json:"position,omitempty"`
	Autofix      *bool   `json:"autofix,omitempty"`
}

func initiativePath(ref string) string {
	return "/issues/initiatives/" + url.PathEscape(ref)
}

// ListInitiatives returns initiatives in the order to work them. An empty
// status takes the API's default, the active ones.
func (c *Client) ListInitiatives(ctx context.Context, initiativeStatus, sort string, limit *int) ([]Initiative, error) {
	query := url.Values{}
	if initiativeStatus != "" {
		query.Set("status", initiativeStatus)
	}
	if sort != "" {
		query.Set("sort", sort)
	}
	applyLimit(query, limit)
	var initiatives []Initiative
	if err := c.get(ctx, withQuery("/issues/initiatives", query), &initiatives); err != nil {
		return nil, err
	}
	return initiatives, nil
}

// GetInitiative returns one initiative by any reference the API resolves.
func (c *Client) GetInitiative(ctx context.Context, ref string) (Initiative, error) {
	var initiative Initiative
	if err := c.get(ctx, initiativePath(ref), &initiative); err != nil {
		return Initiative{}, err
	}
	return initiative, nil
}

// CreateInitiative starts an initiative.
func (c *Client) CreateInitiative(ctx context.Context, in InitiativeCreateInput) (Initiative, error) {
	var initiative Initiative
	if err := c.send(ctx, http.MethodPost, "/issues/initiatives", in, &initiative); err != nil {
		return Initiative{}, err
	}
	return initiative, nil
}

// UpdateInitiative applies a partial update, sending each field in clear as null.
func (c *Client) UpdateInitiative(ctx context.Context, ref string, in InitiativeUpdateInput, clear []string) (Initiative, error) {
	body, err := mergeClearNulls(in, clear)
	if err != nil {
		return Initiative{}, err
	}
	var initiative Initiative
	if err := c.send(ctx, http.MethodPatch, initiativePath(ref), body, &initiative); err != nil {
		return Initiative{}, err
	}
	return initiative, nil
}

// DeleteInitiative removes an initiative. Its issues stay, belonging to none.
func (c *Client) DeleteInitiative(ctx context.Context, ref string) error {
	return c.send(ctx, http.MethodDelete, initiativePath(ref), nil, nil)
}

// IssueLabel is one value of the label vocabulary. Labels sharing a group are
// exclusive: an issue carries at most one from each group.
type IssueLabel struct {
	Slug           string  `json:"slug"`
	GroupSlug      *string `json:"group_slug"`
	Description    *string `json:"description"`
	OpenIssueCount int     `json:"open_issue_count"`
}

// IssueLabelCreateInput is the body for POST /issues/labels.
type IssueLabelCreateInput struct {
	Slug        string  `json:"slug"`
	GroupSlug   *string `json:"group_slug,omitempty"`
	Description *string `json:"description,omitempty"`
}

// IssueLabelUpdateInput is a partial update. A label's slug is its name in
// every issue that carries it, so it does not change.
type IssueLabelUpdateInput struct {
	GroupSlug   *string `json:"group_slug,omitempty"`
	Description *string `json:"description,omitempty"`
}

func labelPath(slug string) string {
	return "/issues/labels/" + url.PathEscape(slug)
}

// ListIssueLabels returns the whole label vocabulary, grouped.
func (c *Client) ListIssueLabels(ctx context.Context) ([]IssueLabel, error) {
	var labels []IssueLabel
	if err := c.get(ctx, "/issues/labels", &labels); err != nil {
		return nil, err
	}
	return labels, nil
}

// GetIssueLabel returns one label by slug.
func (c *Client) GetIssueLabel(ctx context.Context, slug string) (IssueLabel, error) {
	var label IssueLabel
	if err := c.get(ctx, labelPath(slug), &label); err != nil {
		return IssueLabel{}, err
	}
	return label, nil
}

// CreateIssueLabel adds a label to the vocabulary.
func (c *Client) CreateIssueLabel(ctx context.Context, in IssueLabelCreateInput) (IssueLabel, error) {
	var label IssueLabel
	if err := c.send(ctx, http.MethodPost, "/issues/labels", in, &label); err != nil {
		return IssueLabel{}, err
	}
	return label, nil
}

// UpdateIssueLabel applies a partial update, sending each field in clear as null.
func (c *Client) UpdateIssueLabel(ctx context.Context, slug string, in IssueLabelUpdateInput, clear []string) (IssueLabel, error) {
	body, err := mergeClearNulls(in, clear)
	if err != nil {
		return IssueLabel{}, err
	}
	var label IssueLabel
	if err := c.send(ctx, http.MethodPatch, labelPath(slug), body, &label); err != nil {
		return IssueLabel{}, err
	}
	return label, nil
}

// DeleteIssueLabel removes a label from the vocabulary and from every issue.
func (c *Client) DeleteIssueLabel(ctx context.Context, slug string) error {
	return c.send(ctx, http.MethodDelete, labelPath(slug), nil, nil)
}
