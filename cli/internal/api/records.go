package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// The kinds of record that share one number sequence.
const (
	RecordKindIdea       = "idea"
	RecordKindInitiative = "initiative"
	RecordKindIssue      = "issue"
)

// RecordKinds is what --kind accepts, in the order a record moves through them.
var RecordKinds = []string{RecordKindIdea, RecordKindInitiative, RecordKindIssue}

// RecordParts is what every kind's read carries beside its own fields: every
// claim the record has had and every comment on it, oldest first, and its
// documents.
type RecordParts struct {
	Claims    []Claim           `json:"claims"`
	Comments  []Comment         `json:"comments"`
	Documents []DocumentSummary `json:"documents"`
}

// Record is the read of any record by its number. Kind says which of Idea,
// Initiative and Issue holds it, and the other two are nil.
type Record struct {
	Kind       string
	Number     int
	Idea       *IdeaDetail
	Initiative *InitiativeDetail
	Issue      *IssueDetail
}

// UnmarshalJSON decodes `record` into the read its `kind` names, so a caller
// never decodes one kind's fields as another's.
func (r *Record) UnmarshalJSON(data []byte) error {
	var envelope struct {
		Kind   string          `json:"kind"`
		Number int             `json:"number"`
		Record json.RawMessage `json:"record"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	*r = Record{Kind: envelope.Kind, Number: envelope.Number}
	var target any
	switch envelope.Kind {
	case RecordKindIdea:
		r.Idea = &IdeaDetail{}
		target = r.Idea
	case RecordKindInitiative:
		r.Initiative = &InitiativeDetail{}
		target = r.Initiative
	case RecordKindIssue:
		r.Issue = &IssueDetail{}
		target = r.Issue
	default:
		return fmt.Errorf("record #%d is of kind %q, which this CLI does not know", envelope.Number, envelope.Kind)
	}
	return json.Unmarshal(envelope.Record, target)
}

// GetRecord returns the record holding a number, whatever its kind.
func (c *Client) GetRecord(ctx context.Context, ref string, zone DayZone) (Record, error) {
	var record Record
	if err := c.get(ctx, withQuery("/records/"+url.PathEscape(ref), zoneQuery(zone)), &record); err != nil {
		return Record{}, err
	}
	return record, nil
}

// RecordListed is one record of any kind in a list across kinds. Status is nil
// for an idea, which has none.
type RecordListed struct {
	Number    int       `json:"number"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Status    *string   `json:"status"`
	Priority  int       `json:"priority"`
	CreatedTS time.Time `json:"created_ts"`
}

// RecordFilter narrows a list across kinds. A zero field narrows nothing.
type RecordFilter struct {
	Search string
	Kind   string
}

// ListRecords returns records of every kind and status whose title or
// description holds every word of the search: unclosed first, then the most
// urgent, then the newest.
func (c *Client) ListRecords(ctx context.Context, filter RecordFilter, limit *int) ([]RecordListed, error) {
	query := url.Values{}
	if filter.Search != "" {
		query.Set("search", filter.Search)
	}
	if filter.Kind != "" {
		query.Set("kind", filter.Kind)
	}
	applyLimit(query, limit)
	var records []RecordListed
	if err := c.get(ctx, withQuery("/records", query), &records); err != nil {
		return nil, err
	}
	return records, nil
}
