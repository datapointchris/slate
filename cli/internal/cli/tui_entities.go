package cli

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/gotermboard"

	"github.com/datapointchris/slate/cli/internal/api"
)

// The kinds of thing a tab's line names. t goes from a line to the one tab declaring the kind,
// which draws the thing whole, and * locks every tab to it. A kind is declared on one tab, because
// gotermboard asks which tab where two declare it. Each record kind is spelled as slate spells it,
// and its id is its number, since the record kinds share one sequence.
const (
	// entityKindIdea lands on the ideas tab.
	entityKindIdea = api.RecordKindIdea
	// entityKindInitiative lands on the initiatives tab.
	entityKindInitiative = api.RecordKindInitiative
	// entityKindIssue lands on the initiatives tab, under its initiative.
	entityKindIssue = api.RecordKindIssue
	// entityKindPursuit is a pursuit, which a line names beside each record serving it.
	entityKindPursuit = api.RecordKindPursuit
	// entityKindWorklog is a worklog, by its name: its record's number and its sequence, such as 12.1.
	entityKindWorklog = "worklog"
	// entityKindPullRequest is a pull request, by its link as a worklog recorded it, which
	// prsOnPullRequest opens.
	entityKindPullRequest = "pull request"
)

// recordEntity is the slate record numbered number, of kind.
func recordEntity(kind string, number int) gotermboard.EntityRef {
	return gotermboard.EntityRef{Kind: kind, ID: strconv.Itoa(number)}
}

// recordAndPursuits is a record and each pursuit it serves, as its line names them.
func recordAndPursuits(kind string, number int, pursuits []api.PursuitSummary) []gotermboard.EntityRef {
	refs := []gotermboard.EntityRef{recordEntity(kind, number)}
	for _, pursuit := range pursuits {
		refs = append(refs, recordEntity(entityKindPursuit, pursuit.Number))
	}
	return refs
}

// soughtNumber is the number of the record that ref names, where ref is of kind.
func soughtNumber(ref *gotermboard.EntityRef, kind string) (int, bool) {
	if ref == nil || ref.Kind != kind {
		return 0, false
	}
	number, err := strconv.Atoi(ref.ID)
	return number, err == nil
}

// prsOnPullRequest is t on a pull request: prs opens on its link, and quitting prs comes back.
// gotermboard asks CannotOpen first, so Command opens only a link it passed.
var prsOnPullRequest = gotermboard.Outside{
	Kind: entityKindPullRequest, Name: "prs",
	CannotOpen: prsCannotOpen,
	Command: func(link string) tea.ExecCommand {
		// prs holds the terminal until the user quits it, so the handover has no deadline.
		return terminalCommand{exec.CommandContext(context.Background(), "prs", "tui", "--pr", link)}
	},
}

// prsCannotOpen says why prs cannot open link, and nothing where link is a web link and prs is
// on PATH.
func prsCannotOpen(link string) string {
	if !webLink(link) {
		return fmt.Sprintf("%s is not a web link, so nothing opened it", link)
	}
	if _, err := exec.LookPath("prs"); err != nil {
		return fmt.Sprintf("prs is not on PATH, so o opens %s in the browser", pullRequestName(link))
	}
	return ""
}

// terminalCommand is a command that Bubble Tea's Exec hands the terminal to.
type terminalCommand struct{ *exec.Cmd }

func (c terminalCommand) SetStdin(r io.Reader)  { c.Stdin = r }
func (c terminalCommand) SetStdout(w io.Writer) { c.Stdout = w }
func (c terminalCommand) SetStderr(w io.Writer) { c.Stderr = w }
