package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/datapointchris/slate/cli/internal/api"
)

// A queued issue whose paths meet a held issue's says so on its row, so the
// collision shows before anyone takes it.
func TestNext_ARowMeetingAHeldIssuesPathsNamesIt(t *testing.T) {
	repo := "webapp"
	row := api.QueueRow{
		Kind: api.RecordKindIssue, Number: 412, Title: "Log each request", Step: api.QueueStepBuild, Priority: 2,
		Overlaps: []api.PathOverlap{{Number: 398, Holder: "agent-a", Paths: []string{"src/api"}}},
		Issue:    &api.Issue{Number: 412, Title: "Log each request", Repo: &repo, Status: api.IssueStatusOpen, Priority: 2, EffectivePriority: 2},
	}
	var out bytes.Buffer
	printQueueTable(&out, []api.QueueRow{row}, false, time.Now())
	if !strings.Contains(out.String(), "Log each request  [overlaps #398 held by agent-a on src/api]") {
		t.Errorf("printed:\n%s\nwant the row to name #398, its holder and the path that meets", out.String())
	}
}
