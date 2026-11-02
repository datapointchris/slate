package api

import (
	"context"
	"net/http"
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
