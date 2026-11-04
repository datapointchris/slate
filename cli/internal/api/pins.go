package api

import (
	"context"
	"net/http"
)

// PinInput is the body for pinning a record: the agent session doing it, where
// one is, and the host it runs on.
type PinInput struct {
	Session *string `json:"session,omitempty"`
	Host    *string `json:"host,omitempty"`
}

// PinIssue holds an issue ahead of every priority until it is unpinned or closed.
func (c *Client) PinIssue(ctx context.Context, ref string, pin PinInput, zone DayZone) (IssueWritten, error) {
	var written IssueWritten
	if err := c.send(ctx, http.MethodPost, withQuery(issuePath(ref, "pin"), zoneQuery(zone)), pin, &written); err != nil {
		return IssueWritten{}, err
	}
	return written, nil
}

// UnpinIssue returns an issue to the place its priority and rank give it.
func (c *Client) UnpinIssue(ctx context.Context, ref string, zone DayZone) (IssueWritten, error) {
	var written IssueWritten
	if err := c.send(ctx, http.MethodDelete, withQuery(issuePath(ref, "pin"), zoneQuery(zone)), nil, &written); err != nil {
		return IssueWritten{}, err
	}
	return written, nil
}

// PinInitiative holds an initiative and its issues ahead of every priority
// until it is unpinned, finished or shelved.
func (c *Client) PinInitiative(ctx context.Context, ref string, pin PinInput) (InitiativeWritten, error) {
	var written InitiativeWritten
	if err := c.send(ctx, http.MethodPost, initiativePath(ref)+"/pin", pin, &written); err != nil {
		return InitiativeWritten{}, err
	}
	return written, nil
}

// UnpinInitiative returns an initiative to the place its priority and rank give it.
func (c *Client) UnpinInitiative(ctx context.Context, ref string) (InitiativeWritten, error) {
	var written InitiativeWritten
	if err := c.send(ctx, http.MethodDelete, initiativePath(ref)+"/pin", nil, &written); err != nil {
		return InitiativeWritten{}, err
	}
	return written, nil
}

// PinIdea holds an idea ahead of every priority until it is unpinned or promoted.
func (c *Client) PinIdea(ctx context.Context, ref string, pin PinInput) (IdeaWritten, error) {
	var written IdeaWritten
	if err := c.send(ctx, http.MethodPost, ideaPath(ref)+"/pin", pin, &written); err != nil {
		return IdeaWritten{}, err
	}
	return written, nil
}

// UnpinIdea returns an idea to the place its priority and rank give it.
func (c *Client) UnpinIdea(ctx context.Context, ref string) (IdeaWritten, error) {
	var written IdeaWritten
	if err := c.send(ctx, http.MethodDelete, ideaPath(ref)+"/pin", nil, &written); err != nil {
		return IdeaWritten{}, err
	}
	return written, nil
}
