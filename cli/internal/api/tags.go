package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Tag is one value of the short closed list every kind's --tag takes. The
// counts are of open records carrying it themselves; an issue reading its
// initiative's tag is counted on the initiative.
type Tag struct {
	Name                string    `json:"name"`
	Description         string    `json:"description"`
	CreatedTS           time.Time `json:"created_ts"`
	OpenRecordCount     int       `json:"open_record_count"`
	OpenIdeaCount       int       `json:"open_idea_count"`
	OpenInitiativeCount int       `json:"open_initiative_count"`
	OpenIssueCount      int       `json:"open_issue_count"`
}

// TagCreateInput is the body for POST /tags.
type TagCreateInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// TagUpdateInput renames a tag, changes its description, or both. Every
// record carrying it keeps it under the new name.
type TagUpdateInput struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

// TagWritten is the answer to a write on a tag.
type TagWritten struct {
	Tag Tag `json:"tag"`
	WriteEffects
}

// TagDeleted is the answer to deleting a tag: the tag as it stood, with each
// record it came off named as a side effect.
type TagDeleted struct {
	Deleted Tag `json:"deleted"`
	WriteEffects
}

func tagPath(name string) string {
	return "/tags/" + url.PathEscape(name)
}

// ListTags returns every tag in name order.
func (c *Client) ListTags(ctx context.Context, limit *int) ([]Tag, error) {
	query := url.Values{}
	applyLimit(query, limit)
	var tags []Tag
	if err := c.get(ctx, withQuery("/tags", query), &tags); err != nil {
		return nil, err
	}
	return tags, nil
}

// GetTag returns one tag by name.
func (c *Client) GetTag(ctx context.Context, name string) (Tag, error) {
	var tag Tag
	if err := c.get(ctx, tagPath(name), &tag); err != nil {
		return Tag{}, err
	}
	return tag, nil
}

// CreateTag adds a tag to the list.
func (c *Client) CreateTag(ctx context.Context, in TagCreateInput) (TagWritten, error) {
	var written TagWritten
	if err := c.send(ctx, http.MethodPost, "/tags", in, &written); err != nil {
		return TagWritten{}, err
	}
	return written, nil
}

// UpdateTag renames a tag or changes its description.
func (c *Client) UpdateTag(ctx context.Context, name string, in TagUpdateInput) (TagWritten, error) {
	var written TagWritten
	if err := c.send(ctx, http.MethodPatch, tagPath(name), in, &written); err != nil {
		return TagWritten{}, err
	}
	return written, nil
}

// DeleteTag removes a tag from the list and from every record carrying it,
// and the answer names each record it came off.
func (c *Client) DeleteTag(ctx context.Context, name string) (TagDeleted, error) {
	var deleted TagDeleted
	if err := c.send(ctx, http.MethodDelete, tagPath(name), nil, &deleted); err != nil {
		return TagDeleted{}, err
	}
	return deleted, nil
}
