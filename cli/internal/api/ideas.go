package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// IdeaEvidence is one thing an idea rests on. Kind is "url" for an http or
// https URL and "key" for anything else, derived by the API from Ref.
type IdeaEvidence struct {
	Ref      string    `json:"ref"`
	Kind     string    `json:"kind"`
	Note     *string   `json:"note"`
	LinkedBy *string   `json:"linked_by"`
	LinkedTS time.Time `json:"linked_ts"`
}

// Idea is work nobody has taken on yet. Initiative is the one it was promoted
// into, with that initiative's progress, and nil until then.
type Idea struct {
	ID          string         `json:"id"`
	Number      int            `json:"number"`
	Title       string         `json:"title"`
	Description *string        `json:"description"`
	Priority    int            `json:"priority"`
	CreatedTS   time.Time      `json:"created_ts"`
	UpdatedTS   time.Time      `json:"updated_ts"`
	Evidence    []IdeaEvidence `json:"evidence"`
	Initiative  *Initiative    `json:"initiative"`
	Claim       *Claim         `json:"claim"`
}

// IdeaDetail is an idea with every claim ever held on it and every comment on
// it, oldest first, and its documents.
type IdeaDetail struct {
	Idea
	Claims    []Claim           `json:"claims"`
	Comments  []Comment         `json:"comments"`
	Documents []DocumentSummary `json:"documents"`
}

// IdeaWritten is the answer to an idea write.
type IdeaWritten struct {
	Idea Idea `json:"idea"`
	WriteEffects
}

// IdeaCreateInput is the body for POST /ideas.
type IdeaCreateInput struct {
	Title       string  `json:"title"`
	Description *string `json:"description,omitempty"`
	Priority    *int    `json:"priority,omitempty"`
}

// IdeaUpdateInput is a partial update, cleared the way IssueUpdateInput is.
type IdeaUpdateInput struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Priority    *int    `json:"priority,omitempty"`
}

// IdeaEvidenceInput links each of Refs, with one note and linker for all.
type IdeaEvidenceInput struct {
	Refs     []string `json:"refs"`
	Note     *string  `json:"note,omitempty"`
	LinkedBy *string  `json:"linked_by,omitempty"`
}

// IdeaPromoteInput shapes the initiative a promotion creates. Each nil field
// takes the idea's own, and an empty Status takes active.
type IdeaPromoteInput struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Priority    *int    `json:"priority,omitempty"`
	Status      string  `json:"status,omitempty"`
	Autofix     *bool   `json:"autofix,omitempty"`
}

func ideaPath(ref string) string {
	return "/ideas/" + url.PathEscape(ref)
}

// ListIdeas returns every idea, promoted or not, most urgent first.
func (c *Client) ListIdeas(ctx context.Context, limit *int) ([]Idea, error) {
	query := url.Values{}
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

// LinkIdeaEvidence links evidence to an idea. A ref it already links is left
// as it was, and the answer warns.
func (c *Client) LinkIdeaEvidence(ctx context.Context, ref string, in IdeaEvidenceInput) (IdeaWritten, error) {
	var written IdeaWritten
	if err := c.send(ctx, http.MethodPost, ideaPath(ref)+"/evidence", in, &written); err != nil {
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
