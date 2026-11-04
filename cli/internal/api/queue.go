package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// The steps a queue row asks of whoever takes it.
const (
	QueueStepDiscuss = "discuss"
	QueueStepPlan    = "plan"
	QueueStepDecide  = "decide"
	QueueStepBuild   = "build"
)

// QueueSteps is what --step accepts, in the order a piece of work moves through them.
var QueueSteps = []string{QueueStepDiscuss, QueueStepPlan, QueueStepDecide, QueueStepBuild}

// QueueFilter narrows the queue. Step keeps one step's rows. Every
// ReadyFilter field but Initiative reads only issues, so setting one leaves
// every idea and initiative out.
type QueueFilter struct {
	ReadyFilter
	Step string
}

// PathOverlap is another issue under a live claim whose paths meet a row's:
// who holds it, and its paths that meet.
type PathOverlap struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Holder    string    `json:"holder"`
	ExpiresTS time.Time `json:"expires_ts"`
	Paths     []string  `json:"paths"`
}

// QueueRow is one record in the queue, of any kind. Priority is the one it
// sorts at. Holder is set on a claimed row. Record holds whichever of Issue,
// Initiative and Idea Kind names, and the other two are nil.
type QueueRow struct {
	Kind       string        `json:"kind"`
	Number     int           `json:"number"`
	Title      string        `json:"title"`
	Step       string        `json:"step"`
	Priority   int           `json:"priority"`
	Pinned     bool          `json:"pinned"`
	Holder     *string       `json:"holder"`
	Overlaps   []PathOverlap `json:"overlaps"`
	Record     any           `json:"record"`
	Issue      *Issue        `json:"-"`
	Initiative *Initiative   `json:"-"`
	Idea       *Idea         `json:"-"`
}

// UnmarshalJSON decodes `record` into the read its `kind` names.
func (r *QueueRow) UnmarshalJSON(data []byte) error {
	type fields QueueRow
	var envelope struct {
		fields
		Record json.RawMessage `json:"record"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	*r = QueueRow(envelope.fields)
	switch r.Kind {
	case RecordKindIssue:
		r.Issue = &Issue{}
		r.Record = r.Issue
	case RecordKindInitiative:
		r.Initiative = &Initiative{}
		r.Record = r.Initiative
	case RecordKindIdea:
		r.Idea = &Idea{}
		r.Record = r.Idea
	default:
		return fmt.Errorf("queue row #%d is of kind %q, which this CLI does not know", r.Number, r.Kind)
	}
	return json.Unmarshal(envelope.Record, r.Record)
}

// Queue is the unheld work in the order to take it, first row next, and the
// held work apart, by holder.
type Queue struct {
	Queue   []QueueRow `json:"queue"`
	Claimed []QueueRow `json:"claimed"`
}

// GetQueue returns every kind of open work by priority, each row with its
// step, and the held work apart.
func (c *Client) GetQueue(ctx context.Context, filter QueueFilter, zone DayZone) (Queue, error) {
	query := filter.query()
	if filter.Step != "" {
		query.Set("step", filter.Step)
	}
	setZone(query, zone)
	var queue Queue
	if err := c.get(ctx, withQuery("/queue", query), &queue); err != nil {
		return Queue{}, err
	}
	return queue, nil
}
