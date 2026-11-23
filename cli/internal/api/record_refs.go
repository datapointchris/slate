package api

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// RecordRef is one key or URL that a record names as what it came from or
// rests on. Kind is the API's reading of Ref, "url" or "key".
type RecordRef struct {
	Ref      string    `json:"ref"`
	Kind     string    `json:"kind"`
	Note     *string   `json:"note"`
	LinkedBy *string   `json:"linked_by"`
	LinkedTS time.Time `json:"linked_ts"`
}

// RecordRefsInput links each of Refs, with one note and linker for all. A nil
// LinkedBy signs each with the caller.
type RecordRefsInput struct {
	Refs     []string `json:"refs"`
	Note     *string  `json:"note,omitempty"`
	LinkedBy *string  `json:"linked_by,omitempty"`
}

// LinkIssueRefs links refs to an issue. A ref it already carries is left as
// it was, and the answer warns.
func (c *Client) LinkIssueRefs(ctx context.Context, ref string, in RecordRefsInput, zone DayZone) (IssueWritten, error) {
	var written IssueWritten
	if err := c.send(ctx, http.MethodPost, withQuery(issuePath(ref)+"/refs", zoneQuery(zone)), in, &written); err != nil {
		return IssueWritten{}, err
	}
	return written, nil
}

// LinkInitiativeRefs links refs to an initiative. A ref it already carries
// is left as it was, and the answer warns.
func (c *Client) LinkInitiativeRefs(ctx context.Context, ref string, in RecordRefsInput) (InitiativeWritten, error) {
	var written InitiativeWritten
	if err := c.send(ctx, http.MethodPost, initiativePath(ref)+"/refs", in, &written); err != nil {
		return InitiativeWritten{}, err
	}
	return written, nil
}

// LinkIdeaRefs links refs to an idea. A ref it already carries is left as it
// was, and the answer warns. An API from before refs answers /refs with 404
// and takes the same body at /evidence, so a 404 is sent there.
func (c *Client) LinkIdeaRefs(ctx context.Context, ref string, in RecordRefsInput) (IdeaWritten, error) {
	var written IdeaWritten
	err := c.send(ctx, http.MethodPost, ideaPath(ref)+"/refs", in, &written)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.NotFound() {
		err = c.send(ctx, http.MethodPost, ideaPath(ref)+"/evidence", in, &written)
	}
	if err != nil {
		return IdeaWritten{}, err
	}
	return written, nil
}
