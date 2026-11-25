package cli

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/gotermboard"

	"github.com/datapointchris/slate/cli/internal/api"
)

// pullRequestOpens is what a pull request's line says its keys do, on every tab that draws one.
const pullRequestOpens = "enter opens it in prs, and o in the browser"

// workedSession is one session's spans in one worklog, summed.
type workedSession struct {
	id        string
	directory string
	started   time.Time
	ended     *time.Time
	tokens    *api.Tokens
}

// sessionsIn is each session that joined the worklog, in the order each first joined. A
// session that left and joined again is one, ended only where its every span is.
func sessionsIn(worklog api.Worklog) []workedSession {
	var sessions []workedSession
	at := map[string]int{}
	for _, span := range worklog.Spans {
		i, seen := at[span.Session]
		if !seen {
			at[span.Session] = len(sessions)
			sessions = append(sessions, workedSession{id: span.Session, directory: strValue(span.Directory), started: span.StartedTS, ended: span.EndedTS})
			i = len(sessions) - 1
		}
		session := &sessions[i]
		switch {
		case span.EndedTS == nil:
			session.ended = nil
		case session.ended != nil && span.EndedTS.After(*session.ended):
			session.ended = span.EndedTS
		}
		if span.Tokens != nil {
			summed := *span.Tokens
			if session.tokens != nil {
				summed = session.tokens.Plus(summed)
			}
			session.tokens = &summed
		}
	}
	return sessions
}

// shortSession is a session id's first group, as a person tells sessions apart.
func shortSession(id string) string {
	first, _, _ := strings.Cut(id, "-")
	return first
}

// shortTokenCount writes a count of tokens short, as 19.9k, 1.2M or 1.2B.
func shortTokenCount(tokens float64) string {
	switch {
	case tokens >= 1e9:
		return strconv.FormatFloat(tokens/1e9, 'f', 1, 64) + "B"
	case tokens >= 1e6:
		return strconv.FormatFloat(tokens/1e6, 'f', 1, 64) + "M"
	case tokens >= 1e3:
		return strconv.FormatFloat(tokens/1e3, 'f', 1, 64) + "k"
	}
	return strconv.FormatFloat(math.Round(tokens), 'f', 0, 64)
}

// pullRequestName is a pull request's link as owner/name#number, where its path reads as
// GitHub's, Bitbucket Cloud's or Bitbucket Server's does, with or without a tab after the
// number, and otherwise the link whole.
func pullRequestName(link string) string {
	parsed, err := url.Parse(link)
	if err != nil {
		return link
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	switch {
	case len(parts) >= 6 && parts[0] == "projects" && parts[2] == "repos" && parts[4] == "pull-requests":
		return parts[1] + "/" + parts[3] + "#" + parts[5]
	case len(parts) >= 4 && (parts[2] == "pull" || parts[2] == "pull-requests"):
		return parts[0] + "/" + parts[1] + "#" + parts[3]
	}
	return link
}

// webLink says whether link opens a web page, the one kind of link the board hands on.
// A worklog's link is free text any writer sets, and a desktop opener honors file, ssh and
// every scheme an app registers.
func webLink(link string) bool {
	parsed, err := url.Parse(link)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != ""
}

// linkOpened is what came of o, for the board whose model opened it. Every tab's board
// receives it, so each says only its own.
type linkOpened struct {
	model *gotermboard.Model
	link  string
	err   error
}

func (o linkOpened) sentence() string {
	if o.err != nil {
		return fmt.Sprintf("no browser opened %s: %v", pullRequestName(o.link), o.err)
	}
	return "opened " + pullRequestName(o.link) + " in the browser"
}

// sayLinkOpened says what came of o where msg is the end of one m started.
func sayLinkOpened(m *gotermboard.Model, msg tea.Msg) {
	if opened, ok := msg.(linkOpened); ok && opened.model == m {
		m.Say(opened.sentence())
	}
}

// openWebLink opens link in the browser where it is a web link, and otherwise names it in the
// footer, since a link may be a hash or free text. The opener runs as a command, off the
// program's goroutine, because a launcher may wait on the browser it starts.
func openWebLink(m *gotermboard.Model, link string) tea.Cmd {
	if !webLink(link) {
		m.Say(fmt.Sprintf("%s is not a web link, so nothing opened it", link))
		return nil
	}
	return func() tea.Msg { return linkOpened{model: m, link: link, err: openQuietly(link)} }
}
