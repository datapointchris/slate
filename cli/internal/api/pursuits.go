package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// PursuitStatuses is what a pursuit's --status accepts.
var PursuitStatuses = []string{"active", "someday", "completed", "dropped", "all"}

// PursuitSummary is a pursuit as a record in it reads it: the number a
// write sets, and the name a person knows.
type PursuitSummary struct {
	Number int    `json:"number"`
	Name   string `json:"name"`
}

// InPursuits is the pursuits a record read carries, in number order.
// Directions is the same list under the name the release before pursuits
// served, so a reader of that name keeps reading it until a later release
// drops it.
type InPursuits struct {
	Pursuits   []PursuitSummary `json:"pursuits"`
	Directions []PursuitSummary `json:"directions"`
}

// Pursuit is a condition the work should bring about and hold. Measure is the
// command that reads where it stands, nil where nothing measures it yet. Ideas
// and Initiatives are each record in it, in number order, whatever its status.
// A pursuit holds no work and lends no priority.
type Pursuit struct {
	ID           string          `json:"id"`
	Number       int             `json:"number"`
	Name         string          `json:"name"`
	Summary      string          `json:"summary"`
	Description  *string         `json:"description"`
	Measure      *string         `json:"measure"`
	Status       string          `json:"status"`
	StatusReason *string         `json:"status_reason"`
	CreatedTS    time.Time       `json:"created_ts"`
	ClosedTS     *time.Time      `json:"closed_ts"`
	Ideas        []RecordSummary `json:"ideas"`
	Initiatives  []RecordSummary `json:"initiatives"`
}

// PursuitDetail is a pursuit with its claims, comments and documents.
type PursuitDetail struct {
	Pursuit
	RecordParts
}

// PursuitCreateInput is the body for POST /pursuits.
type PursuitCreateInput struct {
	Name         string  `json:"name"`
	Summary      string  `json:"summary"`
	Description  *string `json:"description,omitempty"`
	Measure      *string `json:"measure,omitempty"`
	Status       *string `json:"status,omitempty"`
	StatusReason *string `json:"status_reason,omitempty"`
}

// PursuitUpdateInput is a partial update, cleared the way IssueUpdateInput
// is. Every record in the pursuit stays in it.
type PursuitUpdateInput struct {
	Name         *string `json:"name,omitempty"`
	Summary      *string `json:"summary,omitempty"`
	Description  *string `json:"description,omitempty"`
	Measure      *string `json:"measure,omitempty"`
	Status       *string `json:"status,omitempty"`
	StatusReason *string `json:"status_reason,omitempty"`
}

// PursuitWritten is the answer to a write on a pursuit.
type PursuitWritten struct {
	Pursuit Pursuit `json:"pursuit"`
	WriteEffects
}

func pursuitPath(ref string) string {
	return "/pursuits/" + url.PathEscape(ref)
}

// ListPursuits returns the pursuits at a status, active first. An empty
// status leaves the API's default, the active ones.
func (c *Client) ListPursuits(ctx context.Context, status string, limit *int) ([]Pursuit, error) {
	query := url.Values{}
	if status != "" {
		query.Set("status", status)
	}
	applyLimit(query, limit)
	var pursuits []Pursuit
	if err := c.get(ctx, withQuery("/pursuits", query), &pursuits); err != nil {
		return nil, err
	}
	return pursuits, nil
}

// GetPursuit returns one pursuit by its number or its exact name.
func (c *Client) GetPursuit(ctx context.Context, ref string) (PursuitDetail, error) {
	var pursuit PursuitDetail
	if err := c.get(ctx, pursuitPath(ref), &pursuit); err != nil {
		return PursuitDetail{}, err
	}
	return pursuit, nil
}

// CreatePursuit adds a pursuit.
func (c *Client) CreatePursuit(ctx context.Context, in PursuitCreateInput) (PursuitWritten, error) {
	var written PursuitWritten
	if err := c.send(ctx, http.MethodPost, "/pursuits", in, &written); err != nil {
		return PursuitWritten{}, err
	}
	return written, nil
}

// UpdatePursuit applies a partial update, sending each field in clear as null.
func (c *Client) UpdatePursuit(ctx context.Context, ref string, in PursuitUpdateInput, clear []string) (PursuitWritten, error) {
	body, err := mergeClearNulls(in, clear)
	if err != nil {
		return PursuitWritten{}, err
	}
	var written PursuitWritten
	if err := c.send(ctx, http.MethodPatch, pursuitPath(ref), body, &written); err != nil {
		return PursuitWritten{}, err
	}
	return written, nil
}
