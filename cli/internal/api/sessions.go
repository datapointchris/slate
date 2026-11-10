package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Tokens is a count of tokens by kind, never summed across kinds.
type Tokens struct {
	UncachedInput int `json:"uncached_input"`
	CacheWrite    int `json:"cache_write"`
	CacheRead     int `json:"cache_read"`
	Output        int `json:"output"`
}

// TokenKinds are the API keys of the kinds Tokens counts, in its order.
var TokenKinds = []string{"uncached_input", "cache_write", "cache_read", "output"}

// TokenEstimate is the tokens by kind a piece of work is expected to spend. A
// nil kind was not estimated, which differs from an estimate of zero.
type TokenEstimate struct {
	UncachedInput *int `json:"uncached_input"`
	CacheWrite    *int `json:"cache_write"`
	CacheRead     *int `json:"cache_read"`
	Output        *int `json:"output"`
}

// MessageTokens is one model message a session spent tokens on. Key is the
// provider's message id, or where the message was read when it carries none,
// and is unique across sessions.
type MessageTokens struct {
	Key       string    `json:"key"`
	WrittenTS time.Time `json:"written_ts"`
	Model     *string   `json:"model,omitempty"`
	Subagent  *string   `json:"subagent,omitempty"`
	Tokens    Tokens    `json:"tokens"`
}

// SessionUsageReport is every message a session has spent tokens on so far.
// It replaces whatever the session reported before.
type SessionUsageReport struct {
	Host     *string         `json:"host,omitempty"`
	Messages []MessageTokens `json:"messages"`
}

// SessionSpan is a session's time in one worklog, and the tokens it spent
// there: nil while no message it reported landed there.
type SessionSpan struct {
	Worklog      string     `json:"worklog"`
	RecordNumber int        `json:"record_number"`
	StartedTS    time.Time  `json:"started_ts"`
	EndedTS      *time.Time `json:"ended_ts"`
	Tokens       *Tokens    `json:"tokens"`
}

// AgentSession is a session slate has seen, with its tokens by kind in all,
// in each worklog span, and outside every worklog, as it last reported them.
// Each sum is nil while no message it covers was reported.
type AgentSession struct {
	ID                    string        `json:"id"`
	Host                  *string       `json:"host"`
	FirstSeenTS           time.Time     `json:"first_seen_ts"`
	UsageReportedTS       *time.Time    `json:"usage_reported_ts"`
	MessageCount          int           `json:"message_count"`
	Tokens                *Tokens       `json:"tokens"`
	TokensOutsideWorklogs *Tokens       `json:"tokens_outside_worklogs"`
	Spans                 []SessionSpan `json:"spans"`
}

// SessionWritten is the answer to a usage report.
type SessionWritten struct {
	Session AgentSession `json:"session"`
	WriteEffects
}

func sessionPath(id, action string) string {
	path := "/sessions/" + url.PathEscape(id)
	if action != "" {
		path += "/" + action
	}
	return path
}

// GetSession reads one agent session with its tokens.
func (c *Client) GetSession(ctx context.Context, id string) (AgentSession, error) {
	var session AgentSession
	if err := c.get(ctx, sessionPath(id, ""), &session); err != nil {
		return AgentSession{}, err
	}
	return session, nil
}

// ReportSessionUsage replaces the tokens the session spent with the report's.
// The answer's messages give each time in zone.
func (c *Client) ReportSessionUsage(ctx context.Context, id string, report SessionUsageReport, zone DayZone) (SessionWritten, error) {
	if report.Messages == nil {
		report.Messages = []MessageTokens{}
	}
	var written SessionWritten
	if err := c.send(ctx, http.MethodPut, withQuery(sessionPath(id, "usage"), zoneQuery(zone)), report, &written); err != nil {
		return SessionWritten{}, err
	}
	return written, nil
}
