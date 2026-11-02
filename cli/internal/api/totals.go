package api

import (
	"context"
	"time"
)

// TotalsScope is what a read of the work's totals covered, as it named it.
// Each field left nil narrows nothing.
type TotalsScope struct {
	Record  *RecordSummary `json:"record"`
	Worklog *string        `json:"worklog"`
	Repo    *string        `json:"repo"`
	Since   *time.Time     `json:"since"`
	Until   *time.Time     `json:"until"`
}

// ModelTokens is one model's messages and their tokens by kind, so a reader
// can price each kind at that model's rate.
type ModelTokens struct {
	Model        *string `json:"model"`
	MessageCount int     `json:"message_count"`
	Tokens       Tokens  `json:"tokens"`
}

// WorkTotals is the worklogs, sessions, links, messages and tokens by kind
// over one scope, each derived by the API on every read. Tokens is nil while
// no message it covers was reported. TokensOutsideWorklogs is the part of
// Tokens no worklog held, which only a scope naming no record, worklog or repo
// reaches.
type WorkTotals struct {
	Scope                 TotalsScope   `json:"scope"`
	Worklogs              int           `json:"worklogs"`
	Sessions              int           `json:"sessions"`
	Commits               int           `json:"commits"`
	PullRequests          int           `json:"pull_requests"`
	Landings              int           `json:"landings"`
	MessageCount          int           `json:"message_count"`
	Tokens                *Tokens       `json:"tokens"`
	TokensOutsideWorklogs *Tokens       `json:"tokens_outside_worklogs"`
	TokensByModel         []ModelTokens `json:"tokens_by_model"`
}

// TotalsFilter narrows the worklogs a totals read reaches. Record and Worklog
// are sent when set. A nil Repo narrows nothing, and a pointer to "" reaches
// the issues on no repo.
type TotalsFilter struct {
	Record  string
	Worklog string
	Repo    *string
}

// GetTotals reads the totals of the work the filter reaches inside window.
func (c *Client) GetTotals(ctx context.Context, filter TotalsFilter, window Window) (WorkTotals, error) {
	query := repoQuery(filter.Repo)
	if filter.Record != "" {
		query.Set("record", filter.Record)
	}
	if filter.Worklog != "" {
		query.Set("worklog", filter.Worklog)
	}
	applyWindow(query, window)
	var totals WorkTotals
	if err := c.get(ctx, withQuery("/totals", query), &totals); err != nil {
		return WorkTotals{}, err
	}
	return totals, nil
}
