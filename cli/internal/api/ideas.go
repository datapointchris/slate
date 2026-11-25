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
	Refs       []RecordRef `json:"refs"`
	Initiative *Initiative `json:"initiative"`
	Claim      *Claim      `json:"claim"`
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

// The kinds an idea is promoted to, as IdeaPromoteInput.Kind names them.
const (
	PromotedToInitiative = "initiative"
	PromotedToIssue      = "issue"
)

// PromoteKinds are what an idea can be promoted to, the first the default.
var PromoteKinds = []string{PromotedToInitiative, PromotedToIssue}

// PromoteFieldsOfKind are the IdeaPromoteInput fields, by JSON name, that apply
// to one kind alone. The API refuses one sent with the other kind.
var PromoteFieldsOfKind = map[string][]string{
	PromotedToInitiative: {"autofix", "name", "not_planned_steps", "pursuits", "status", "summary"},
	PromotedToIssue:      {"acceptance", "initiative", "parent", "paths", "repo", "type"},
}

// PromoteSharedFields are the IdeaPromoteInput fields, by JSON name, that apply
// to either kind.
var PromoteSharedFields = []string{"description", "priority", "tags"}

// IdeaPromoteInput is what an idea becomes under its own number: Kind and the
// fields of that kind. The API refuses a field of the other kind, and an API
// that does not promote in place refuses Kind itself, before it writes. An
// initiative needs Summary, because an idea has none. Each nil field keeps what
// the record holds, and an empty Status takes active.
type IdeaPromoteInput struct {
	Kind        string    `json:"kind"`
	Description *string   `json:"description,omitempty"`
	Priority    *int      `json:"priority,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`

	Summary  string  `json:"summary,omitempty"`
	Name     *string `json:"name,omitempty"`
	Status   string  `json:"status,omitempty"`
	Autofix  *bool   `json:"autofix,omitempty"`
	Pursuits *[]int  `json:"pursuits,omitempty"`
	// NotPlannedSteps left empty plans every step, because an idea marks none.
	NotPlannedSteps []string `json:"not_planned_steps,omitempty"`

	Repo       *string  `json:"repo,omitempty"`
	Paths      []string `json:"paths,omitempty"`
	Type       *string  `json:"type,omitempty"`
	Acceptance *string  `json:"acceptance,omitempty"`
	Initiative *string  `json:"initiative,omitempty"`
	Parent     *string  `json:"parent,omitempty"`
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

// ListIdeas returns every idea, most urgent first. A promoted idea is its
// initiative or issue under the same number, so it is not listed.
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

// PromotedInitiativeWritten is a promotion's answer to an initiative. Idea is
// the idea naming the initiative it became, in the shape that CLI releases
// through cli/v0.66.0 read from a promotion.
type PromotedInitiativeWritten struct {
	InitiativeWritten
	Idea *Idea `json:"idea,omitempty"`
}

// PromoteIdea turns an idea into an initiative under its own number. A number
// another kind holds is refused. The answer's messages give each time in zone.
func (c *Client) PromoteIdea(ctx context.Context, ref string, in IdeaPromoteInput, zone DayZone) (PromotedInitiativeWritten, error) {
	in.Kind = PromotedToInitiative
	var written PromotedInitiativeWritten
	if err := c.send(ctx, http.MethodPost, withQuery(ideaPath(ref)+"/promote", zoneQuery(zone)), in, &written); err != nil {
		return PromotedInitiativeWritten{}, err
	}
	return written, nil
}

// PromoteIdeaToIssue turns an idea into an issue under its own number, which
// starts open and drops the idea's pursuits. The answer's messages give each
// time in zone.
func (c *Client) PromoteIdeaToIssue(ctx context.Context, ref string, in IdeaPromoteInput, zone DayZone) (IssueWritten, error) {
	in.Kind = PromotedToIssue
	var written IssueWritten
	if err := c.send(ctx, http.MethodPost, withQuery(ideaPath(ref)+"/promote", zoneQuery(zone)), in, &written); err != nil {
		return IssueWritten{}, err
	}
	return written, nil
}

// IdeaFoldInput is the body for POST /ideas/{id}/fold. Holder names whose
// claim on the idea moves to the target, or ends where it holds the target too.
type IdeaFoldInput struct {
	Into   string `json:"into"`
	Holder string `json:"holder,omitempty"`
}

// FoldWritten is a fold's answer: the target in its own kind's envelope, so
// exactly one of Idea, Initiative and Issue is set.
type FoldWritten struct {
	Idea       *Idea        `json:"idea,omitempty"`
	Initiative *Initiative  `json:"initiative,omitempty"`
	Issue      *IssueDetail `json:"issue,omitempty"`
	WriteEffects
}

// FoldIdea folds an idea into the idea, initiative or issue that covers its
// work, moving every row there and leading the idea's number there. The
// answer's messages give each time in zone.
func (c *Client) FoldIdea(ctx context.Context, ref string, in IdeaFoldInput, zone DayZone) (FoldWritten, error) {
	var written FoldWritten
	if err := c.send(ctx, http.MethodPost, withQuery(ideaPath(ref)+"/fold", zoneQuery(zone)), in, &written); err != nil {
		return FoldWritten{}, err
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
