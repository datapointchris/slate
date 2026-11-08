package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/gotermboard"
	"github.com/pkg/browser"
	"go.opentelemetry.io/otel/baggage"

	"github.com/datapointchris/slate/cli/internal/repos"
)

// sessionBrief opens the first message of the Claude session C opens on a record. It names the
// record by number alone, so the session reads the record's words as data with `slate show`.
const sessionBrief = "This is slate %[1]s %[2]d, opened from `slate tui`. `slate show %[2]d` prints it whole."

// claude is C: a Claude session on the row's record, in its repo's checkout.
func (b *initiativesBoard) claude(m *gotermboard.Model) tea.Cmd {
	kind, number, _, ok := b.recordAt(m.Cursor())
	if !ok {
		return nil
	}
	directory, fallback, err := b.sessionDirectory(kind, number)
	if err != nil {
		m.Say(err.Error())
		return nil
	}
	return gotermboard.OpenClaudeSession(gotermboard.ClaudeSession{
		On: strconv.Itoa(number), Dir: directory, Fallback: fallback, Brief: fmt.Sprintf(sessionBrief, kind, number),
		Env: sessionTelemetry(number),
	})
}

// sessionTelemetry joins the session C opens to its record and to the flow slate runs in, through
// resource attributes, because an interactive Claude session ignores an incoming trace. The
// record goes under the name slate's spans give it, and each member of the baggage slate
// inherited goes on as it came, so slate names no key of its caller's. The attributes slate
// inherited stay ahead of them.
func sessionTelemetry(number int) []string {
	var attributes []string
	if resource := os.Getenv("OTEL_RESOURCE_ATTRIBUTES"); resource != "" {
		attributes = append(attributes, resource)
	}
	attributes = append(attributes, fmt.Sprintf("slate.record.number=%d", number))
	flow := os.Getenv("BAGGAGE")
	carried, _ := baggage.Parse(flow)
	members := carried.Members()
	slices.SortFunc(members, func(a, b baggage.Member) int { return strings.Compare(a.Key(), b.Key()) })
	for _, member := range members {
		attributes = append(attributes, member.Key()+"="+url.PathEscape(member.Value()))
	}
	env := []string{"OTEL_RESOURCE_ATTRIBUTES=" + strings.Join(attributes, ",")}
	if flow != "" {
		env = append(env, "BAGGAGE="+flow)
	}
	return env
}

// sessionDirectory is where C opens Claude: the checkout the registry names for the record's
// repo, else the board's own directory with why it fell back. A checkout named and missing
// opens nothing, so a session never plans against another repo's code.
func (b *initiativesBoard) sessionDirectory(kind string, number int) (directory, fallback string, err error) {
	repo := b.repoOf(kind, number)
	if repo != "" {
		registry, err := repos.Load(repos.DefaultPath())
		if err != nil {
			return "", "", err
		}
		if checkout, named := registry.Checkout(repo); named {
			if info, err := os.Stat(checkout); err != nil || !info.IsDir() {
				return "", "", fmt.Errorf("%s is not checked out at %s, so no session opened on %d", repo, checkout, number)
			}
			return checkout, "", nil
		}
	}
	here, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("no directory to open a session on %d in: %w", number, err)
	}
	if repo == "" {
		return here, fmt.Sprintf("%d names no one repo", number), nil
	}
	return here, "the repo registry names no checkout of " + repo, nil
}

// repoOf is the one repo a record's work is in: an issue's, or an initiative's where its
// issues name only one.
func (b *initiativesBoard) repoOf(kind string, number int) string {
	if kind == "issue" {
		issue, _ := b.issueAt(issueKey(number))
		return strValue(issue.Repo)
	}
	for _, initiative := range b.initiatives.Value().Initiatives {
		if initiative.Number == number && len(initiative.Repos) == 1 {
			return initiative.Repos[0]
		}
	}
	return ""
}

// usageExit is the status a command line it refuses exits with, by the convention prs keeps.
const usageExit = 2

// openInPrs is enter on a pull request's row: prs opens on it, and quitting prs comes back.
// After prs exits non-zero, the footer shows the last line it wrote to stderr, or its exit
// status where it wrote nothing.
func (b *initiativesBoard) openInPrs(m *gotermboard.Model, link string) tea.Cmd {
	name := pullRequestName(link)
	if !webLink(link) {
		m.Say(fmt.Sprintf("%s is not a web link, so nothing opened it", link))
		return nil
	}
	if _, err := exec.LookPath("prs"); err != nil {
		m.Say(fmt.Sprintf("prs is not on PATH, so o opens %s in the browser", name))
		return nil
	}
	return m.HandOver(exec.Command("prs", "tui", "--pr", link), func(m *gotermboard.Model, err error, stderr []byte) tea.Cmd {
		var exited *exec.ExitError
		switch {
		case errors.As(err, &exited) && exited.ExitCode() == usageExit:
			m.Say(fmt.Sprintf("the prs on PATH refused `prs tui --pr` (%s), so o opens %s in the browser", gotermboard.LastLine(stderr, err), name))
		case err != nil:
			m.Say(fmt.Sprintf("prs did not open %s: %s", name, gotermboard.LastLine(stderr, err)))
		default:
			m.Say("back from prs on " + name)
		}
		return nil
	})
}

// webLink says whether link opens a web page, the one kind of link the board hands on.
// A worklog's link is free text any writer sets, and a desktop opener honors file, ssh and
// every scheme an app registers.
func webLink(link string) bool {
	parsed, err := url.Parse(link)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != ""
}

// openInBrowser is o on a pull request's row.
func (b *initiativesBoard) openInBrowser(m *gotermboard.Model) tea.Cmd {
	link, ok := pullRequestAt(m.Cursor())
	if !ok {
		return nil
	}
	if !webLink(link) {
		m.Say(fmt.Sprintf("%s is not a web link, so nothing opened it", link))
		return nil
	}
	// The browser's own output would draw over the board.
	browser.Stdout, browser.Stderr = io.Discard, io.Discard
	if err := browser.OpenURL(link); err != nil {
		m.Say(fmt.Sprintf("no browser opened %s: %v", pullRequestName(link), err))
		return nil
	}
	m.Say("opened " + pullRequestName(link) + " in the browser")
	return nil
}

// pullRequestKeyPrefix leads a pull request row's key, which ends in the link as recorded.
const pullRequestKeyPrefix = "pr:"

func pullRequestKey(issue int, link string) string {
	return pullRequestKeyPrefix + strconv.Itoa(issue) + ":" + link
}

// pullRequestAt is the link a pull request row's key holds.
func pullRequestAt(key string) (string, bool) {
	rest, found := strings.CutPrefix(key, pullRequestKeyPrefix)
	if !found {
		return "", false
	}
	_, link, found := strings.Cut(rest, ":")
	return link, found
}

func onPullRequest(m *gotermboard.Model) bool {
	_, ok := pullRequestAt(m.Cursor())
	return ok
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
