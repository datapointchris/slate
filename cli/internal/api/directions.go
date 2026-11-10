package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// DirectionStatuses is what a direction's --status accepts.
var DirectionStatuses = []string{"active", "someday", "completed", "dropped", "all"}

// DirectionSummary is a direction as a record in it reads it: the number a
// write sets, and the name a person knows.
type DirectionSummary struct {
	Number int    `json:"number"`
	Name   string `json:"name"`
}

// Direction is one way the work moves toward its vision, and where it ends.
// Ideas and Initiatives are each record in it, in number order, whatever its
// status. A direction holds no work and lends no priority.
type Direction struct {
	ID           string          `json:"id"`
	Number       int             `json:"number"`
	Name         string          `json:"name"`
	Summary      string          `json:"summary"`
	Description  *string         `json:"description"`
	Status       string          `json:"status"`
	StatusReason *string         `json:"status_reason"`
	CreatedTS    time.Time       `json:"created_ts"`
	ClosedTS     *time.Time      `json:"closed_ts"`
	Ideas        []RecordSummary `json:"ideas"`
	Initiatives  []RecordSummary `json:"initiatives"`
}

// DirectionDetail is a direction with its claims, comments and documents.
type DirectionDetail struct {
	Direction
	RecordParts
}

// DirectionCreateInput is the body for POST /directions.
type DirectionCreateInput struct {
	Name         string  `json:"name"`
	Summary      string  `json:"summary"`
	Description  *string `json:"description,omitempty"`
	Status       *string `json:"status,omitempty"`
	StatusReason *string `json:"status_reason,omitempty"`
}

// DirectionUpdateInput is a partial update, cleared the way IssueUpdateInput
// is. Every record in the direction stays in it.
type DirectionUpdateInput struct {
	Name         *string `json:"name,omitempty"`
	Summary      *string `json:"summary,omitempty"`
	Description  *string `json:"description,omitempty"`
	Status       *string `json:"status,omitempty"`
	StatusReason *string `json:"status_reason,omitempty"`
}

// DirectionWritten is the answer to a write on a direction.
type DirectionWritten struct {
	Direction Direction `json:"direction"`
	WriteEffects
}

func directionPath(ref string) string {
	return "/directions/" + url.PathEscape(ref)
}

// ListDirections returns the directions at a status, active first. An empty
// status leaves the API's default, the active ones.
func (c *Client) ListDirections(ctx context.Context, status string, limit *int) ([]Direction, error) {
	query := url.Values{}
	if status != "" {
		query.Set("status", status)
	}
	applyLimit(query, limit)
	var directions []Direction
	if err := c.get(ctx, withQuery("/directions", query), &directions); err != nil {
		return nil, err
	}
	return directions, nil
}

// GetDirection returns one direction by its number or its exact name.
func (c *Client) GetDirection(ctx context.Context, ref string) (DirectionDetail, error) {
	var direction DirectionDetail
	if err := c.get(ctx, directionPath(ref), &direction); err != nil {
		return DirectionDetail{}, err
	}
	return direction, nil
}

// CreateDirection adds a direction.
func (c *Client) CreateDirection(ctx context.Context, in DirectionCreateInput) (DirectionWritten, error) {
	var written DirectionWritten
	if err := c.send(ctx, http.MethodPost, "/directions", in, &written); err != nil {
		return DirectionWritten{}, err
	}
	return written, nil
}

// UpdateDirection applies a partial update, sending each field in clear as null.
func (c *Client) UpdateDirection(ctx context.Context, ref string, in DirectionUpdateInput, clear []string) (DirectionWritten, error) {
	body, err := mergeClearNulls(in, clear)
	if err != nil {
		return DirectionWritten{}, err
	}
	var written DirectionWritten
	if err := c.send(ctx, http.MethodPatch, directionPath(ref), body, &written); err != nil {
		return DirectionWritten{}, err
	}
	return written, nil
}
