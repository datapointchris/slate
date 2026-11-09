package api

import (
	"context"
	"net/http"
	"net/url"
)

// AddDependency records that the issue or initiative ref names waits on
// dependsOn. The API reads which kind ref names, and refuses an edge that would
// leave a record waiting on itself.
func (c *Client) AddDependency(ctx context.Context, ref, dependsOn string, zone DayZone) (DependencyWritten, error) {
	var written DependencyWritten
	body := map[string]string{"depends_on": dependsOn}
	if err := c.send(ctx, http.MethodPost, withQuery(dependenciesPath(ref), zoneQuery(zone)), body, &written); err != nil {
		return DependencyWritten{}, err
	}
	return written, nil
}

// RemoveDependency removes the edge from ref to dependsOn, and answers the
// issue or initiative with what it still waits on.
func (c *Client) RemoveDependency(ctx context.Context, ref, dependsOn string, zone DayZone) (DependencyWritten, error) {
	var written DependencyWritten
	path := dependenciesPath(ref) + "/" + url.PathEscape(dependsOn)
	if err := c.send(ctx, http.MethodDelete, withQuery(path, zoneQuery(zone)), nil, &written); err != nil {
		return DependencyWritten{}, err
	}
	return written, nil
}

func dependenciesPath(ref string) string {
	return "/records/" + url.PathEscape(ref) + "/dependencies"
}
