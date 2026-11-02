package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// The states a worklog reads as, the same three a claim does, derived by the
// API on every read. A live one is under way, an expired one's claim ran out
// and no write has ended it yet, and an ended one is history.
const (
	WorklogStateLive    = "live"
	WorklogStateExpired = "expired"
	WorklogStateEnded   = "ended"
	WorklogStateAll     = "all"
)

// WorklogStates is what `worklogs list --state` accepts.
var WorklogStates = []string{WorklogStateLive, WorklogStateExpired, WorklogStateEnded, WorklogStateAll}

// WorklogWorkflows are the ways a worklog's work was approached.
var WorklogWorkflows = []string{"oneshot", "plan-first", "design-first"}

// The kinds of thing a worklog links, in the order a change reaches main.
const (
	WorklogLinkCommit      = "commit"
	WorklogLinkPullRequest = "pull_request"
	WorklogLinkLanding     = "landing"
)

// WorklogLinkKinds is every kind `worklogs link` sends.
var WorklogLinkKinds = []string{WorklogLinkCommit, WorklogLinkPullRequest, WorklogLinkLanding}

// WorklogSpan is one agent session's time in a worklog, and the tokens it
// spent there. EndedTS is nil while the session is still in it.
type WorklogSpan struct {
	Session   string     `json:"session"`
	Directory *string    `json:"directory"`
	StartedTS time.Time  `json:"started_ts"`
	EndedTS   *time.Time `json:"ended_ts"`
	Tokens    Tokens     `json:"tokens"`
}

// WorklogLink is a commit, pull request or landing a worklog produced, as given.
type WorklogLink struct {
	Kind     string    `json:"kind"`
	Ref      string    `json:"ref"`
	Repo     *string   `json:"repo"`
	Session  *string   `json:"session"`
	LinkedTS time.Time `json:"linked_ts"`
	LinkedBy string    `json:"linked_by"`
}

// Worklog is one go at one record, named <record>.<sequence>. State, Stage
// and Tokens are derived by the API on every read. Claim is the claim whose
// hold opened it, and Waits every stretch its holder spent waiting. Tokens
// sums its spans', as each session last reported them.
type Worklog struct {
	Name         string        `json:"name"`
	RecordNumber int           `json:"record_number"`
	Sequence     int           `json:"sequence"`
	Record       RecordSummary `json:"record"`
	OpenedBy     string        `json:"opened_by"`
	Workflow     *string       `json:"workflow"`
	StartedTS    time.Time     `json:"started_ts"`
	EndedTS      *time.Time    `json:"ended_ts"`
	Outcome      *string       `json:"outcome"`
	EndedBy      *string       `json:"ended_by"`
	State        string        `json:"state"`
	Stage        string        `json:"stage"`
	Claim        *Claim        `json:"claim"`
	Waits        []ClaimWait   `json:"waits"`
	Tokens       Tokens        `json:"tokens"`
	Spans        []WorklogSpan `json:"spans"`
	Links        []WorklogLink `json:"links"`
}

// WorklogWritten is the answer to a write on a worklog.
type WorklogWritten struct {
	Worklog Worklog `json:"worklog"`
	WriteEffects
}

// AgentPlace is the agent session a worklog write moves, and where it runs.
// A nil Session moves no session.
type AgentPlace struct {
	Session   *string `json:"session,omitempty"`
	Host      *string `json:"host,omitempty"`
	Directory *string `json:"directory,omitempty"`
}

// WorklogStartInput opens a worklog with no claim.
type WorklogStartInput struct {
	Record   string  `json:"record"`
	OpenedBy string  `json:"opened_by"`
	Workflow *string `json:"workflow,omitempty"`
	AgentPlace
}

// WorklogLinkInput records one thing a worklog produced.
type WorklogLinkInput struct {
	Kind     string  `json:"kind"`
	Ref      string  `json:"ref"`
	Repo     *string `json:"repo,omitempty"`
	Session  *string `json:"session,omitempty"`
	LinkedBy string  `json:"linked_by"`
}

// WorklogFilter narrows a list of worklogs. A zero field narrows nothing, and
// an empty State reads the API's default, the live ones.
type WorklogFilter struct {
	Record  string
	Session string
	State   string
}

func worklogPath(name, action string) string {
	path := "/worklogs/" + url.PathEscape(name)
	if action != "" {
		path += "/" + action
	}
	return path
}

// ListWorklogs returns worklogs, latest started first.
func (c *Client) ListWorklogs(ctx context.Context, filter WorklogFilter, limit *int) ([]Worklog, error) {
	query := url.Values{}
	for name, value := range map[string]string{"record": filter.Record, "session": filter.Session, "state": filter.State} {
		if value != "" {
			query.Set(name, value)
		}
	}
	applyLimit(query, limit)
	var worklogs []Worklog
	if err := c.get(ctx, withQuery("/worklogs", query), &worklogs); err != nil {
		return nil, err
	}
	return worklogs, nil
}

// GetWorklog reads one worklog by its name.
func (c *Client) GetWorklog(ctx context.Context, name string) (Worklog, error) {
	var worklog Worklog
	if err := c.get(ctx, worklogPath(name, ""), &worklog); err != nil {
		return Worklog{}, err
	}
	return worklog, nil
}

// CurrentWorklog is the live worklog the agent session is in now, or nil when
// it is in none.
func (c *Client) CurrentWorklog(ctx context.Context, session string) (*Worklog, error) {
	var worklog *Worklog
	if err := c.get(ctx, withQuery("/worklogs/current", url.Values{"session": {session}}), &worklog); err != nil {
		return nil, err
	}
	return worklog, nil
}

// StartWorklog opens a worklog on a record with no claim. The answer's
// messages give each time in zone.
func (c *Client) StartWorklog(ctx context.Context, in WorklogStartInput, zone DayZone) (WorklogWritten, error) {
	var written WorklogWritten
	if err := c.send(ctx, http.MethodPost, withQuery("/worklogs", zoneQuery(zone)), in, &written); err != nil {
		return WorklogWritten{}, err
	}
	return written, nil
}

// JoinWorklog puts the agent session in the worklog, ending its span in any
// other.
func (c *Client) JoinWorklog(ctx context.Context, name string, place AgentPlace, zone DayZone) (WorklogWritten, error) {
	var written WorklogWritten
	if err := c.send(ctx, http.MethodPost, withQuery(worklogPath(name, "join"), zoneQuery(zone)), place, &written); err != nil {
		return WorklogWritten{}, err
	}
	return written, nil
}

// FinishWorklog ends the worklog by hand.
func (c *Client) FinishWorklog(ctx context.Context, name, finishedBy string, zone DayZone) (WorklogWritten, error) {
	body := struct {
		FinishedBy string `json:"finished_by"`
	}{finishedBy}
	var written WorklogWritten
	if err := c.send(ctx, http.MethodPost, withQuery(worklogPath(name, "finish"), zoneQuery(zone)), body, &written); err != nil {
		return WorklogWritten{}, err
	}
	return written, nil
}

// LinkWorklog records a commit, pull request or landing on the worklog.
func (c *Client) LinkWorklog(ctx context.Context, name string, in WorklogLinkInput, zone DayZone) (WorklogWritten, error) {
	var written WorklogWritten
	if err := c.send(ctx, http.MethodPost, withQuery(worklogPath(name, "links"), zoneQuery(zone)), in, &written); err != nil {
		return WorklogWritten{}, err
	}
	return written, nil
}

// SetWorklogWorkflow sets how the worklog's work was approached. A nil
// workflow clears it.
func (c *Client) SetWorklogWorkflow(ctx context.Context, name string, workflow *string) (WorklogWritten, error) {
	body := struct {
		Workflow *string `json:"workflow"`
	}{workflow}
	var written WorklogWritten
	if err := c.send(ctx, http.MethodPatch, worklogPath(name, ""), body, &written); err != nil {
		return WorklogWritten{}, err
	}
	return written, nil
}
