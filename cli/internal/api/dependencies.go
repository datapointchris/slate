package api

import (
	"context"
	"net/http"
	"net/url"
)

// AddIssueDependency records that ref waits on dependsOn. The API refuses an
// edge that would leave an issue waiting on itself.
func (c *Client) AddIssueDependency(ctx context.Context, ref, dependsOn string, zone DayZone) (IssueWritten, error) {
	var written IssueWritten
	body := map[string]string{"depends_on": dependsOn}
	if err := c.send(ctx, http.MethodPost, withQuery(issuePath(ref, "dependencies"), zoneQuery(zone)), body, &written); err != nil {
		return IssueWritten{}, err
	}
	return written, nil
}

// RemoveIssueDependency removes the edge from ref to dependsOn, and answers
// the issue with what it still waits on.
func (c *Client) RemoveIssueDependency(ctx context.Context, ref, dependsOn string, zone DayZone) (IssueWritten, error) {
	var written IssueWritten
	if err := c.send(ctx, http.MethodDelete, withQuery(issuePath(ref, "dependencies", dependsOn), zoneQuery(zone)), nil, &written); err != nil {
		return IssueWritten{}, err
	}
	return written, nil
}

// AddInitiativeDependency records that every unclosed issue in the initiative
// ref names waits while the initiative or issue dependsOn names is open. The
// API refuses a wait that would leave a record waiting on itself.
func (c *Client) AddInitiativeDependency(ctx context.Context, ref, dependsOn string) (InitiativeWritten, error) {
	var written InitiativeWritten
	body := map[string]string{"depends_on": dependsOn}
	if err := c.send(ctx, http.MethodPost, initiativePath(ref)+"/dependencies", body, &written); err != nil {
		return InitiativeWritten{}, err
	}
	return written, nil
}

// RemoveInitiativeDependency removes the initiative's wait on dependsOn, and
// answers the initiative with what it still waits on.
func (c *Client) RemoveInitiativeDependency(ctx context.Context, ref, dependsOn string) (InitiativeWritten, error) {
	var written InitiativeWritten
	path := initiativePath(ref) + "/dependencies/" + url.PathEscape(dependsOn)
	if err := c.send(ctx, http.MethodDelete, path, nil, &written); err != nil {
		return InitiativeWritten{}, err
	}
	return written, nil
}
