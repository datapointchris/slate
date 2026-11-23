package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Idea is work nobody has taken on yet. Initiative is the one it was promoted
// into, with that initiative's progress, and nil until then.
type Idea struct {
	ID            string     `json:"id"`
	Number        int        `json:"number"`
	Title         string     `json:"title"`
	Description   *string    `json:"description"`
	Priority      int        `json:"priority"`
	Rank          float64    `json:"rank"`
	PinnedTS      *time.Time `json:"pinned_ts"`
	PinnedBy      *string    `json:"pinned_by"`
	PinnedSession *string    `json:"pinned_session"`
	FlowID        *string    `json:"flow_id"`
	CreatedTS     time.Time  `json:"created_ts"`
	UpdatedTS     time.Time  `json:"updated_ts"`
	Tags          []string   `json:"tags"`
	// InPursuits holds each pursuit the idea is in.
	InPursuits
	Refs []RecordRef `json:"refs"`
	// Evidence is Refs again, under the name that the release before refs
	// served, so a reader of `slate ideas list --json` finds it there too.
	Evidence   []RecordRef `json:"evidence"`
	Initiative *Initiative `json:"initiative"`
	Claim      *Claim      `json:"claim"`
}

// RefsOrEvidence is the idea's refs, read from Evidence when the answer
// carried no refs field, as an API from before refs answers.
func (i Idea) RefsOrEvidence() []RecordRef {
	if i.Refs == nil {
		return i.Evidence
	}
	return i.Refs
}

// IdeaDetail is an idea with its claims, comments and documents.
type IdeaDetail struct {
	Idea
	RecordParts
}

// IdeaWritten is the answer to an idea write.
type IdeaWritten struct {
	Idea Idea `json:"idea"`
	WriteEffects
}

// IdeaCreateInput is the body for POST /ideas. Refs are linked as the caller.
type IdeaCreateInput struct {
	Title       string   `json:"title"`
	Description *string  `json:"description,omitempty"`
	Priority    *int     `json:"priority,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Pursuits    []int    `json:"pursuits,omitempty"`
	Refs        []string `json:"refs,omitempty"`
}

// IdeaUpdateInput is a partial update, cleared the way IssueUpdateInput is.
// Tags and Pursuits each replace the whole set.
type IdeaUpdateInput struct {
	Title       *string   `json:"title,omitempty"`
	Description *string   `json:"description,omitempty"`
	Priority    *int      `json:"priority,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`
	Pursuits    *[]int    `json:"pursuits,omitempty"`
}

// IdeaPromoteInput shapes the initiative a promotion creates. An idea has no
// summary, so Summary is required. Each nil field takes the idea's own, and an
// empty Status takes active.
type IdeaPromoteInput struct {
	Summary     string    `json:"summary"`
	Name        *string   `json:"name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Priority    *int      `json:"priority,omitempty"`
	Status      string    `json:"status,omitempty"`
	Autofix     *bool     `json:"autofix,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`
	Pursuits    *[]int    `json:"pursuits,omitempty"`
	// NotPlannedSteps left empty plans every step, because an idea marks none.
	NotPlannedSteps []string `json:"not_planned_steps,omitempty"`
}

func ideaPath(ref string) string {
	return "/ideas/" + url.PathEscape(ref)
}

// IdeaFilter narrows a list of ideas. A zero field narrows nothing. Ref is a
// prefix: the ideas carrying a ref that starts with it.
type IdeaFilter struct {
	Tag     string
	Pursuit string
	Ref     string
}

// ListIdeas returns every idea, promoted or not, most urgent first.
func (c *Client) ListIdeas(ctx context.Context, filter IdeaFilter, limit *int) ([]Idea, error) {
	query := url.Values{}
	if filter.Tag != "" {
		query.Set("tag", filter.Tag)
	}
	if filter.Pursuit != "" {
		query.Set("pursuit", filter.Pursuit)
	}
	if filter.Ref != "" {
		query.Set("ref", filter.Ref)
	}
	applyLimit(query, limit)
	var ideas []Idea
	if err := c.get(ctx, withQuery("/ideas", query), &ideas); err != nil {
		return nil, err
	}
	return ideas, nil
}

// GetIdea returns one idea by number or UUID, with its claim history.
func (c *Client) GetIdea(ctx context.Context, ref string) (IdeaDetail, error) {
	var idea IdeaDetail
	if err := c.get(ctx, ideaPath(ref), &idea); err != nil {
		return IdeaDetail{}, err
	}
	return idea, nil
}

// CreateIdea records an idea.
func (c *Client) CreateIdea(ctx context.Context, in IdeaCreateInput) (IdeaWritten, error) {
	var written IdeaWritten
	if err := c.send(ctx, http.MethodPost, "/ideas", in, &written); err != nil {
		return IdeaWritten{}, err
	}
	return written, nil
}

// UpdateIdea applies a partial update, sending each field in clear as null.
func (c *Client) UpdateIdea(ctx context.Context, ref string, in IdeaUpdateInput, clear []string) (IdeaWritten, error) {
	body, err := mergeClearNulls(in, clear)
	if err != nil {
		return IdeaWritten{}, err
	}
	var written IdeaWritten
	if err := c.send(ctx, http.MethodPatch, ideaPath(ref), body, &written); err != nil {
		return IdeaWritten{}, err
	}
	return written, nil
}

// PromoteIdea creates the initiative an idea becomes. A promoted idea is
// refused. The answer's messages give each time in zone.
func (c *Client) PromoteIdea(ctx context.Context, ref string, in IdeaPromoteInput, zone DayZone) (IdeaWritten, error) {
	var written IdeaWritten
	if err := c.send(ctx, http.MethodPost, withQuery(ideaPath(ref)+"/promote", zoneQuery(zone)), in, &written); err != nil {
		return IdeaWritten{}, err
	}
	return written, nil
}

// DeleteIdea removes an idea with its refs, pin and claims. A promoted
// idea is refused, naming its initiative. The answer gives each time in zone.
func (c *Client) DeleteIdea(ctx context.Context, ref string, zone DayZone) (RecordDeleted, error) {
	var deleted RecordDeleted
	if err := c.send(ctx, http.MethodDelete, withQuery(ideaPath(ref), zoneQuery(zone)), nil, &deleted); err != nil {
		return RecordDeleted{}, err
	}
	return deleted, nil
}

// RankIdea places an idea directly before or after another record of its
// priority.
func (c *Client) RankIdea(ctx context.Context, ref string, move RankMove, zone DayZone) (IdeaWritten, error) {
	var written IdeaWritten
	if err := c.send(ctx, http.MethodPost, withQuery(ideaPath(ref)+"/rank", zoneQuery(zone)), move, &written); err != nil {
		return IdeaWritten{}, err
	}
	return written, nil
}
