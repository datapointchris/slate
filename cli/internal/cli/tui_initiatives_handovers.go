package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/gotermboard"
	"github.com/pkg/browser"
)

// tmuxTimeout bounds tmux opening a window, which answers at once on a live server.
const tmuxTimeout = 5 * time.Second

// sessionBrief is the first message of the Claude session C opens on a record.
const sessionBrief = "This is slate %[1]s %[2]d, %[3]s, opened from `slate tui`. " +
	"`slate show %[2]d` prints it whole. Read it, say what it asks for and what you would do, " +
	"then wait before changing anything."

// claude is C: a Claude session on the row's record. Inside tmux it opens in a window of its
// own, so the board stays usable while the session works, and outside tmux it takes the
// board's terminal until it ends.
func (b *initiativesBoard) claude(m *gotermboard.Model) tea.Cmd {
	kind, number, title, ok := b.recordAt(m.Cursor())
	if !ok {
		return nil
	}
	if _, err := exec.LookPath("claude"); err != nil {
		m.Say(fmt.Sprintf("claude is not on PATH, so no session opened on %d", number))
		return nil
	}
	brief := fmt.Sprintf(sessionBrief, kind, number, title)
	if os.Getenv("TMUX") != "" {
		if err := openWindow(fmt.Sprintf("claude %d", number), "claude", brief); err != nil {
			m.Say(err.Error())
			return nil
		}
		m.Say(fmt.Sprintf("opened a Claude session on %d in a tmux window", number))
		return nil
	}
	return b.handOver(exec.Command("claude", brief), func(m *gotermboard.Model, err error, _ string) tea.Cmd {
		b.readAgain()
		if err != nil {
			m.Say(fmt.Sprintf("the Claude session on %d ended with %v", number, err))
			return nil
		}
		m.Say(fmt.Sprintf("back from the Claude session on %d", number))
		return nil
	})
}

// openWindow runs argv in a new tmux window in the board's directory. tmux runs several
// arguments without a shell, so the brief needs no quoting.
func openWindow(name string, argv ...string) error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("TMUX is set and tmux is not on PATH, so no window opened for %s", name)
	}
	args := []string{"new-window", "-n", name}
	if directory, err := os.Getwd(); err == nil {
		args = append(args, "-c", directory)
	}
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "tmux", append(args, argv...)...).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux could not open %s: %s", name, lastLineOf(out, err))
	}
	return nil
}

// handOver gives the terminal to cmd until it exits, keeping what it wrote to stderr, then
// runs then on the board's goroutine.
func (b *initiativesBoard) handOver(cmd *exec.Cmd, then func(m *gotermboard.Model, err error, stderr string) tea.Cmd) tea.Cmd {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return handedBack{board: b, then: func(m *gotermboard.Model) tea.Cmd { return then(m, err, stderr.String()) }}
	})
}

// openInPrs is enter on a pull request's row: prs opens on it, and quitting prs comes back.
// prs names an unreadable pull request in one line on stderr and exits 1.
func (b *initiativesBoard) openInPrs(m *gotermboard.Model, link string) tea.Cmd {
	name := pullRequestName(link)
	if _, err := exec.LookPath("prs"); err != nil {
		m.Say(fmt.Sprintf("prs is not on PATH, so o opens %s in the browser", name))
		return nil
	}
	return b.handOver(exec.Command("prs", "tui", "--pr", link), func(m *gotermboard.Model, err error, stderr string) tea.Cmd {
		if err != nil {
			m.Say(fmt.Sprintf("prs did not open %s: %s", name, lastLineOf([]byte(stderr), err)))
			return nil
		}
		m.Say("back from prs on " + name)
		return nil
	})
}

// openInBrowser is o on a pull request's row.
func (b *initiativesBoard) openInBrowser(m *gotermboard.Model) tea.Cmd {
	link, ok := pullRequestAt(m.Cursor())
	if !ok {
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

// lastLineOf is the last thing a failed command said, or its exit status where it said nothing.
func lastLineOf(out []byte, err error) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return last
	}
	return err.Error()
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
// GitHub's or Bitbucket's does, and otherwise the link whole.
func pullRequestName(link string) string {
	parsed, err := url.Parse(link)
	if err != nil {
		return link
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) == 4 && (parts[2] == "pull" || parts[2] == "pull-requests") {
		return parts[0] + "/" + parts[1] + "#" + parts[3]
	}
	return link
}
