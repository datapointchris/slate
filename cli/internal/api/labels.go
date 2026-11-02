package api

import (
	"context"
	"net/http"
	"net/url"
)

// IssueLabel is one value of the label vocabulary. Labels sharing a group are
// exclusive: an issue carries at most one from each group.
type IssueLabel struct {
	Slug           string  `json:"slug"`
	GroupSlug      *string `json:"group_slug"`
	Description    *string `json:"description"`
	OpenIssueCount int     `json:"open_issue_count"`
}

// IssueLabelCreateInput is the body for POST /labels.
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
	return "/labels/" + url.PathEscape(slug)
}

// ListIssueLabels returns the whole label vocabulary, grouped.
func (c *Client) ListIssueLabels(ctx context.Context) ([]IssueLabel, error) {
	var labels []IssueLabel
	if err := c.get(ctx, "/labels", &labels); err != nil {
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
func (c *Client) CreateIssueLabel(ctx context.Context, in IssueLabelCreateInput) (IssueLabelWritten, error) {
	var written IssueLabelWritten
	if err := c.send(ctx, http.MethodPost, "/labels", in, &written); err != nil {
		return IssueLabelWritten{}, err
	}
	return written, nil
}

// UpdateIssueLabel applies a partial update, sending each field in clear as null.
func (c *Client) UpdateIssueLabel(ctx context.Context, slug string, in IssueLabelUpdateInput, clear []string) (IssueLabelWritten, error) {
	body, err := mergeClearNulls(in, clear)
	if err != nil {
		return IssueLabelWritten{}, err
	}
	var written IssueLabelWritten
	if err := c.send(ctx, http.MethodPatch, labelPath(slug), body, &written); err != nil {
		return IssueLabelWritten{}, err
	}
	return written, nil
}

// DeleteIssueLabel removes a label from the vocabulary and from every issue,
// and the answer names each issue it came off.
func (c *Client) DeleteIssueLabel(ctx context.Context, slug string) (IssueLabelDeleted, error) {
	var deleted IssueLabelDeleted
	if err := c.send(ctx, http.MethodDelete, labelPath(slug), nil, &deleted); err != nil {
		return IssueLabelDeleted{}, err
	}
	return deleted, nil
}
