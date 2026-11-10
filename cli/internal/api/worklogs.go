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

// WorklogSteps are the steps of a record's life a worklog performs, in the
// order a record goes through them.
var WorklogSteps = []string{"shape", "design", "preflight", "build", "retro"}

// CheckedStep is a step checked on a record, read from the latest worklog of
// that step that ended by any outcome but expired. Session is the first agent
// session that worked in it, nil where none did.
type CheckedStep struct {
	Step    string    `json:"step"`
	Worklog string    `json:"worklog"`
	EndedTS time.Time `json:"ended_ts"`
	Session *string   `json:"session"`
}

// The kinds of thing a worklog links, in the order a change reaches main.
const (
	WorklogLinkCommit      = "commit"
	WorklogLinkPullRequest = "pull_request"
	WorklogLinkLanding     = "landing"
)

// WorklogLinkKinds is every kind `worklogs link` sends.
var WorklogLinkKinds = []string{WorklogLinkCommit, WorklogLinkPullRequest, WorklogLinkLanding}

// WorklogSpan is one agent session's time in a worklog, and the tokens it
// spent there. EndedTS is nil while the session is still in it, and Tokens
// while no message the session reported landed in it.
type WorklogSpan struct {
	Session   string     `json:"session"`
	Directory *string    `json:"directory"`
	StartedTS time.Time  `json:"started_ts"`
	EndedTS   *time.Time `json:"ended_ts"`
	Tokens    *Tokens    `json:"tokens"`
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
// sums its spans', as each session last reported them, and is nil while none
// of its spans holds a reported message.
type Worklog struct {
	Name         string        `json:"name"`
	RecordNumber int           `json:"record_number"`
	Sequence     int           `json:"sequence"`
	Record       RecordSummary `json:"record"`
	OpenedBy     string        `json:"opened_by"`
	Step         *string       `json:"step"`
	// Estimate is nil where it estimates no kind.
	Estimate  *TokenEstimate `json:"estimate"`
	StartedTS time.Time      `json:"started_ts"`
	EndedTS   *time.Time     `json:"ended_ts"`
	Outcome   *string        `json:"outcome"`
	EndedBy   *string        `json:"ended_by"`
	State     string         `json:"state"`
	Stage     string         `json:"stage"`
	Claim     *Claim         `json:"claim"`
	Waits     []ClaimWait    `json:"waits"`
	Tokens    *Tokens        `json:"tokens"`
	Spans     []WorklogSpan  `json:"spans"`
	Links     []WorklogLink  `json:"links"`
	// DocumentVersions is every document version a session wrote while in it.
	DocumentVersions []DocumentVersionRef `json:"document_versions"`
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
	Step     *string `json:"step,omitempty"`
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

// WorklogEdit names what a worklog edit changes, and nothing it leaves nil is
// sent. A Step of "" clears the step. Estimate holds each kind to change by
// its API key, such as "output", and a nil count clears that kind.
type WorklogEdit struct {
	Step     *string
	Estimate map[string]*int
}

// EditWorklog sets the step of its record's life the worklog performed, and
// the kinds of its estimate the edit names.
func (c *Client) EditWorklog(ctx context.Context, name string, edit WorklogEdit) (WorklogWritten, error) {
	body := map[string]any{}
	if edit.Step != nil {
		body["step"] = edit.Step
		if *edit.Step == "" {
			body["step"] = nil
		}
	}
	if len(edit.Estimate) > 0 {
		body["estimate"] = edit.Estimate
	}
	var written WorklogWritten
	if err := c.send(ctx, http.MethodPatch, worklogPath(name, ""), body, &written); err != nil {
		return WorklogWritten{}, err
	}
	return written, nil
}
