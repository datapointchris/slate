package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// FocusShareSet is the share of the work a set names for one pursuit, by its
// number, as a fraction from 0 to 1.
type FocusShareSet struct {
	Pursuit int     `json:"pursuit"`
	Share   float64 `json:"share"`
}

// FocusSetInput is the body for PUT /focus. UntilDate is the last day the
// focus holds, YYYY-MM-DD.
type FocusSetInput struct {
	Statement string          `json:"statement"`
	UntilDate string          `json:"until_date"`
	Shares    []FocusShareSet `json:"shares"`
}

type focusEndInput struct {
	Reason string `json:"reason"`
}

// FocusShare is a share as the focus named it.
type FocusShare struct {
	Pursuit PursuitSummary `json:"pursuit"`
	Share   float64        `json:"share"`
}

// Focus is a focus as it was set. Active is derived on read: nothing ended it
// and its UntilDate has not passed. One past its UntilDate reads inactive
// while EndedTS is still null, until the next set ends it as lapsed.
type Focus struct {
	ID          int          `json:"id"`
	Statement   string       `json:"statement"`
	StartedTS   time.Time    `json:"started_ts"`
	UntilDate   string       `json:"until_date"`
	EndedTS     *time.Time   `json:"ended_ts"`
	EndedReason *string      `json:"ended_reason"`
	SetBy       string       `json:"set_by"`
	Active      bool         `json:"active"`
	Shares      []FocusShare `json:"shares"`
}

// FocusWritten is the answer to a set or an end. Focus is nil where an end
// found none in force.
type FocusWritten struct {
	Focus *Focus `json:"focus"`
	WriteEffects
}

// FocusWindow is the span usage is counted over: from the later of the
// focus's start and the API's USAGE_WINDOW before Until. GET /focus ends it at
// the read. A day of GET /focus/usage ends it at that day's midnight, or where
// the focus stopped holding on its last day.
type FocusWindow struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
}

// PursuitFigures is one pursuit's share, output, usage and factor over one
// window. Share and Factor are nil where no focus is in force or the pursuit
// is not active. SplitOutputTokens may hold a fraction, because a worklog
// reaching two pursuits gives each half its output.
type PursuitFigures struct {
	Number            int      `json:"number"`
	Share             *float64 `json:"share"`
	ShareNamed        bool     `json:"share_named"`
	SplitOutputTokens float64  `json:"split_output_tokens"`
	WorklogCount      int      `json:"worklog_count"`
	Usage             float64  `json:"usage"`
	Factor            *float64 `json:"factor"`
}

// PursuitStanding is where one pursuit stands against its share, with its
// name and status now.
type PursuitStanding struct {
	PursuitFigures
	Name      string     `json:"name"`
	Status    string     `json:"status"`
	TouchedTS *time.Time `json:"touched_ts"`
}

// UnattributedUsage is the window's output that reached no pursuit.
type UnattributedUsage struct {
	OutputTokens                int `json:"output_tokens"`
	WorklogCount                int `json:"worklog_count"`
	OutsideWorklogsOutputTokens int `json:"outside_worklogs_output_tokens"`
}

// FocusStanding is the focus in force, nil where none is, and each pursuit's
// standing against it, the furthest under its share first.
type FocusStanding struct {
	Focus        *Focus            `json:"focus"`
	Window       FocusWindow       `json:"window"`
	Pursuits     []PursuitStanding `json:"pursuits"`
	Unattributed UnattributedUsage `json:"unattributed"`
}

// GetFocusStanding reads the focus in force and each pursuit's standing.
func (c *Client) GetFocusStanding(ctx context.Context, zone DayZone) (FocusStanding, error) {
	var standing FocusStanding
	if err := c.get(ctx, withQuery("/focus", zoneQuery(zone)), &standing); err != nil {
		return FocusStanding{}, err
	}
	return standing, nil
}

// FocusUsagePursuit is a pursuit that some day of a usage read lists, with its
// status now.
type FocusUsagePursuit struct {
	PursuitSummary
	Status string `json:"status"`
}

// PursuitUsageOnDay is one pursuit's figures over one day's window.
// SplitOutputTokensOnDay is its part of the output written that day while the
// focus held.
type PursuitUsageOnDay struct {
	PursuitFigures
	SplitOutputTokensOnDay float64 `json:"split_output_tokens_on_day"`
}

// FocusUsageDay is one day of a focus, as YYYY-MM-DD in the read's zone, and
// each pursuit's figures over the window ending with it, the furthest under its
// share first. A pursuit that is not active is listed only on a day whose
// window drew output from it.
type FocusUsageDay struct {
	Day      string              `json:"day"`
	Window   FocusWindow         `json:"window"`
	Pursuits []PursuitUsageOnDay `json:"pursuits"`
}

// FocusUsage is each day of a focus, oldest first, every day from the first to
// the last. Focus is nil where the read named none and none is in force, and
// the days are then the API's last USAGE_DAYS. A focus longer than that answers
// its last USAGE_DAYS, so Since falls after its start. Pursuits names each one
// any day lists, in number order. Only the last day of the focus in force is
// sure to equal GET /focus.
type FocusUsage struct {
	Focus    *Focus              `json:"focus"`
	Since    time.Time           `json:"since"`
	Until    time.Time           `json:"until"`
	Pursuits []FocusUsagePursuit `json:"pursuits"`
	Days     []FocusUsageDay     `json:"days"`
}

// GetFocusUsage reads each day of the focus id names, or of the focus in force
// where id is nil.
func (c *Client) GetFocusUsage(ctx context.Context, id *int, zone DayZone) (FocusUsage, error) {
	query := zoneQuery(zone)
	if id != nil {
		query.Set("focus", strconv.Itoa(*id))
	}
	var usage FocusUsage
	if err := c.get(ctx, withQuery("/focus/usage", query), &usage); err != nil {
		return FocusUsage{}, err
	}
	return usage, nil
}

// ListFocuses returns every focus, the latest set first.
func (c *Client) ListFocuses(ctx context.Context, zone DayZone, limit *int) ([]Focus, error) {
	query := url.Values{}
	setZone(query, zone)
	applyLimit(query, limit)
	var focuses []Focus
	if err := c.get(ctx, withQuery("/focus/history", query), &focuses); err != nil {
		return nil, err
	}
	return focuses, nil
}

// SetFocus sets a focus from now, ending the one in force.
func (c *Client) SetFocus(ctx context.Context, in FocusSetInput, zone DayZone) (FocusWritten, error) {
	if in.Shares == nil {
		in.Shares = []FocusShareSet{}
	}
	var written FocusWritten
	if err := c.send(ctx, http.MethodPut, withQuery("/focus", zoneQuery(zone)), in, &written); err != nil {
		return FocusWritten{}, err
	}
	return written, nil
}

// EndFocus ends the focus in force with a reason.
func (c *Client) EndFocus(ctx context.Context, reason string, zone DayZone) (FocusWritten, error) {
	var written FocusWritten
	if err := c.send(ctx, http.MethodPost, withQuery("/focus/end", zoneQuery(zone)), focusEndInput{reason}, &written); err != nil {
		return FocusWritten{}, err
	}
	return written, nil
}
