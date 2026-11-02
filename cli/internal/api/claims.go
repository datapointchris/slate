package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// The states a claim reads as, derived by the API on every read. A live claim
// holds its record, an expired one ran out unended and frees it, and an ended
// one is history.
const (
	ClaimStateLive    = "live"
	ClaimStateExpired = "expired"
	ClaimStateEnded   = "ended"
	ClaimStateAll     = "all"
)

// ClaimStates is what `claims list --state` accepts.
var ClaimStates = []string{ClaimStateLive, ClaimStateExpired, ClaimStateEnded, ClaimStateAll}

// ClaimWait is a stretch the holder spent waiting while its claim still held
// the record. EndedTS is nil while it still waits.
type ClaimWait struct {
	WaitingOn string     `json:"waiting_on"`
	StartedTS time.Time  `json:"started_ts"`
	EndedTS   *time.Time `json:"ended_ts"`
	EndedBy   *string    `json:"ended_by"`
}

// Claim is one lease on a record of any kind. Ending and EndedTS are set
// together, once it ends. Worklog names the worklog the hold opened, and
// Waiting is what the holder waits on now, nil while it works.
type Claim struct {
	ID           int64      `json:"id"`
	RecordNumber int        `json:"record_number"`
	Holder       string     `json:"holder"`
	Session      *string    `json:"session"`
	Host         *string    `json:"host"`
	Directory    *string    `json:"directory"`
	StartedTS    time.Time  `json:"started_ts"`
	ExpiresTS    time.Time  `json:"expires_ts"`
	EndedTS      *time.Time `json:"ended_ts"`
	Ending       *string    `json:"ending"`
	EndedBy      *string    `json:"ended_by"`
	State        string     `json:"state"`
	Worklog      *string    `json:"worklog"`
	Waiting      *ClaimWait `json:"waiting"`
}

// RecordSummary names a record of any kind on one line. Title is an
// initiative's name.
type RecordSummary struct {
	Number int     `json:"number"`
	Kind   string  `json:"kind"`
	Title  string  `json:"title"`
	Status *string `json:"status"`
}

// ClaimOnRecord is a claim beside the record it is on.
type ClaimOnRecord struct {
	Claim
	Record RecordSummary `json:"record"`
}

// ClaimWritten is the answer to a claim write. Claim is nil when a release
// found nothing held, or a hold of the next issue found nothing ready.
type ClaimWritten struct {
	Claim *ClaimOnRecord `json:"claim"`
	WriteEffects
}

// ClaimLease says who holds a claim, for how long, and where they work. A zero
// Minutes takes the API's default.
type ClaimLease struct {
	Holder    string  `json:"holder"`
	Minutes   int     `json:"minutes,omitempty"`
	Session   *string `json:"session,omitempty"`
	Host      *string `json:"host,omitempty"`
	Directory *string `json:"directory,omitempty"`
}

type claimHoldInput struct {
	Record string `json:"record"`
	ClaimLease
}

type claimHoldNextInput struct {
	ClaimLease
	ReadyFilter
}

// ClaimFilter narrows a list of claims. A zero field narrows nothing, and an
// empty State reads the API's default, the live ones.
type ClaimFilter struct {
	Holder string
	Record string
	State  string
}

func claimPath(record, action string) string {
	return "/claims/" + url.PathEscape(record) + "/" + action
}

// ListClaims returns claims, latest started first.
func (c *Client) ListClaims(ctx context.Context, filter ClaimFilter, limit *int) ([]ClaimOnRecord, error) {
	query := url.Values{}
	for name, value := range map[string]string{"holder": filter.Holder, "record": filter.Record, "state": filter.State} {
		if value != "" {
			query.Set(name, value)
		}
	}
	applyLimit(query, limit)
	var claims []ClaimOnRecord
	if err := c.get(ctx, withQuery("/claims", query), &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

// HoldClaim takes a claim on one record of any kind, refused only by another
// holder's live claim. The zone dates the deferral the answer warns about.
func (c *Client) HoldClaim(ctx context.Context, record string, lease ClaimLease, zone DayZone) (ClaimWritten, error) {
	var written ClaimWritten
	if err := c.send(ctx, http.MethodPost, withQuery("/claims", zoneQuery(zone)), claimHoldInput{record, lease}, &written); err != nil {
		return ClaimWritten{}, err
	}
	return written, nil
}

// HoldNextClaim holds the head of the queue's build step in one request, so two
// callers asking at once never hold the same issue.
func (c *Client) HoldNextClaim(ctx context.Context, lease ClaimLease, filter ReadyFilter, zone DayZone) (ClaimWritten, error) {
	var written ClaimWritten
	path := withQuery("/claims/next", zoneQuery(zone))
	if err := c.send(ctx, http.MethodPost, path, claimHoldNextInput{lease, filter}, &written); err != nil {
		return ClaimWritten{}, err
	}
	return written, nil
}

// ExtendClaim moves the holder's claim on record to minutes from now. The
// answer's messages give each time in zone.
func (c *Client) ExtendClaim(ctx context.Context, record, holder string, minutes int, zone DayZone) (ClaimWritten, error) {
	body := struct {
		Holder  string `json:"holder"`
		Minutes int    `json:"minutes,omitempty"`
	}{holder, minutes}
	var written ClaimWritten
	if err := c.send(ctx, http.MethodPost, withQuery(claimPath(record, "extend"), zoneQuery(zone)), body, &written); err != nil {
		return ClaimWritten{}, err
	}
	return written, nil
}

// WaitOnClaim marks the holder's claim on record as waiting on waitingOn. The
// claim still holds its record.
func (c *Client) WaitOnClaim(ctx context.Context, record, holder, waitingOn string, zone DayZone) (ClaimWritten, error) {
	body := struct {
		Holder    string `json:"holder"`
		WaitingOn string `json:"waiting_on"`
	}{holder, waitingOn}
	var written ClaimWritten
	if err := c.send(ctx, http.MethodPost, withQuery(claimPath(record, "wait"), zoneQuery(zone)), body, &written); err != nil {
		return ClaimWritten{}, err
	}
	return written, nil
}

// ResumeClaim ends the wait on the holder's claim on record, and puts the
// place's session back in the claim's worklog.
func (c *Client) ResumeClaim(ctx context.Context, record, holder string, place AgentPlace, zone DayZone) (ClaimWritten, error) {
	body := struct {
		Holder string `json:"holder"`
		AgentPlace
	}{holder, place}
	var written ClaimWritten
	if err := c.send(ctx, http.MethodPost, withQuery(claimPath(record, "resume"), zoneQuery(zone)), body, &written); err != nil {
		return ClaimWritten{}, err
	}
	return written, nil
}

// ReleaseClaim ends the record's claim as released by releasedBy. The
// answer's messages give each time in zone.
func (c *Client) ReleaseClaim(ctx context.Context, record, releasedBy string, zone DayZone) (ClaimWritten, error) {
	body := struct {
		ReleasedBy string `json:"released_by"`
	}{releasedBy}
	var written ClaimWritten
	if err := c.send(ctx, http.MethodPost, withQuery(claimPath(record, "release"), zoneQuery(zone)), body, &written); err != nil {
		return ClaimWritten{}, err
	}
	return written, nil
}
