package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// CheckCite is one source a check's evidence rests on: its kind, such as
// issue, path or url, and the reference within that kind.
type CheckCite struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

// RecordCheck is one reading of an issue or an initiative against the world it
// describes. CheckedBy is a service's client id, or the actor of the person
// who made it. Run and Session name the scheduled run and agent session that
// made it, where one did.
type RecordCheck struct {
	ID           int         `json:"id"`
	RecordNumber int         `json:"record_number"`
	CheckedTS    time.Time   `json:"checked_ts"`
	CheckedBy    string      `json:"checked_by"`
	Verdict      string      `json:"verdict"`
	Evidence     string      `json:"evidence"`
	Cites        []CheckCite `json:"cites"`
	Run          *string     `json:"run"`
	Session      *string     `json:"session"`
}

// LastCheck is a record's latest check, as every read of an issue or an
// initiative carries it.
type LastCheck struct {
	Verdict   string    `json:"verdict"`
	CheckedTS time.Time `json:"checked_ts"`
	CheckedBy string    `json:"checked_by"`
}

// CheckRequest is someone asking that a record be checked ahead of its
// interval. The next check on the record clears it.
type CheckRequest struct {
	RequestedTS time.Time `json:"requested_ts"`
	RequestedBy string    `json:"requested_by"`
	Reason      string    `json:"reason"`
}

// CheckAddInput is the body for POST /records/{number}/checks.
type CheckAddInput struct {
	Verdict  string      `json:"verdict"`
	Evidence string      `json:"evidence"`
	Cites    []CheckCite `json:"cites"`
	Run      *string     `json:"run,omitempty"`
	Session  *string     `json:"session,omitempty"`
	Host     *string     `json:"host,omitempty"`
}

// RecordCheckWritten is the answer to adding a check.
type RecordCheckWritten struct {
	Check RecordCheck `json:"check"`
	WriteEffects
}

// CheckRequestWritten is the answer to marking a record for a check or
// withdrawing the mark. CheckRequest is the mark after the write, and nil once
// it is withdrawn.
type CheckRequestWritten struct {
	Number       int           `json:"number"`
	CheckRequest *CheckRequest `json:"check_request"`
	WriteEffects
}

// DueCheck is one record due a check. DueBecause is the rung of the due order
// it met first: incoming, requested, never_checked, queue_head or stale.
// Repo is an issue's, and nil on an initiative.
type DueCheck struct {
	Number           int             `json:"number"`
	Kind             string          `json:"kind"`
	Title            string          `json:"title"`
	Status           string          `json:"status"`
	Priority         int             `json:"priority"`
	Repo             *string         `json:"repo"`
	DueBecause       string          `json:"due_because"`
	LastCheck        *LastCheck      `json:"last_check"`
	CheckRequest     *CheckRequest   `json:"check_request"`
	LikelyDuplicates []RecordSummary `json:"likely_duplicates"`
}

func recordChecksPath(ref string) string {
	return "/records/" + url.PathEscape(ref) + "/checks"
}

func checkRequestPath(ref string) string {
	return "/records/" + url.PathEscape(ref) + "/check-request"
}

// ListRecordChecks returns an issue's or an initiative's checks, latest first,
// so a limit keeps the latest.
func (c *Client) ListRecordChecks(ctx context.Context, ref string, limit *int) ([]RecordCheck, error) {
	query := url.Values{}
	applyLimit(query, limit)
	var checks []RecordCheck
	if err := c.get(ctx, withQuery(recordChecksPath(ref), query), &checks); err != nil {
		return nil, err
	}
	return checks, nil
}

// AddRecordCheck records what reading an issue or an initiative found.
func (c *Client) AddRecordCheck(ctx context.Context, ref string, in CheckAddInput) (RecordCheckWritten, error) {
	var written RecordCheckWritten
	if err := c.send(ctx, http.MethodPost, recordChecksPath(ref), in, &written); err != nil {
		return RecordCheckWritten{}, err
	}
	return written, nil
}

// DueChecks returns each open issue and initiative due a check, first due first.
func (c *Client) DueChecks(ctx context.Context, limit *int) ([]DueCheck, error) {
	query := url.Values{}
	applyLimit(query, limit)
	var due []DueCheck
	if err := c.get(ctx, withQuery("/checks/due", query), &due); err != nil {
		return nil, err
	}
	return due, nil
}

// RequestCheck marks an issue or an initiative to be checked next.
func (c *Client) RequestCheck(ctx context.Context, ref, reason string) (CheckRequestWritten, error) {
	var written CheckRequestWritten
	body := struct {
		Reason string `json:"reason"`
	}{Reason: reason}
	if err := c.send(ctx, http.MethodPut, checkRequestPath(ref), body, &written); err != nil {
		return CheckRequestWritten{}, err
	}
	return written, nil
}

// WithdrawCheckRequest clears the mark, leaving the record due at its interval.
func (c *Client) WithdrawCheckRequest(ctx context.Context, ref string) (CheckRequestWritten, error) {
	var written CheckRequestWritten
	if err := c.send(ctx, http.MethodDelete, checkRequestPath(ref), nil, &written); err != nil {
		return CheckRequestWritten{}, err
	}
	return written, nil
}
