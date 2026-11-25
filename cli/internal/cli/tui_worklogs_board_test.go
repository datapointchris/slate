package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/datapointchris/gotermboard"
	"github.com/datapointchris/gotermstyle"

	"github.com/datapointchris/slate/cli/internal/api"
)

const (
	worklogsCommit      = "abc1234def5678abc1234def5678abc1234def56"
	worklogsPullRequest = "https://github.com/owner/repo/pull/40"
)

// worklogsStandIn stands in for slate's worklogs and totals routes. Preflight 4271.2 is live,
// waiting, and links a commit, a pull request and a landing. 2103.1 is live with no step, no
// session and no tokens. 3901.4 expired. The ended read answers as many as it asks for, so every
// bound reads full. Record 4271's totals name 4271.1's estimate.
type worklogsStandIn struct {
	mu    sync.Mutex
	asked []string
}

func (s *worklogsStandIn) serve(w http.ResponseWriter, r *http.Request) {
	day := time.Now().Add(-time.Hour)
	var answer any
	switch r.URL.Path {
	case "/api/v1/worklogs":
		query := r.URL.Query()
		s.mu.Lock()
		s.asked = append(s.asked, query.Get("state")+" "+query.Get("limit"))
		s.mu.Unlock()
		switch query.Get("state") {
		case api.WorklogStateLive:
			answer = []api.Worklog{waitingPreflight(day), quietWorklog(day)}
		case api.WorklogStateExpired:
			answer = []api.Worklog{{
				Name: "3901.4", RecordNumber: 3901, Sequence: 4, Step: new("build"), StartedTS: day, State: api.WorklogStateExpired, Stage: "committed",
				Record: api.RecordSummary{Number: 3901, Kind: api.RecordKindInitiative, Title: "The garden keeps its own journal"},
				Claim:  &api.Claim{ID: 3, RecordNumber: 3901, Holder: "claude-code/8224de10-0c78", Session: new("8224de10-0c78"), StartedTS: day, ExpiresTS: day, State: api.ClaimStateExpired},
			}}
		case api.WorklogStateEnded:
			limit, _ := strconv.Atoi(query.Get("limit"))
			var ended []api.Worklog
			for at := range limit {
				ended = append(ended, api.Worklog{
					Name: fmt.Sprintf("%d.1", 5000+at), RecordNumber: 5000 + at, Sequence: 1, StartedTS: day, EndedTS: &day, HeldUntil: &day,
					Outcome: new("completed"), State: api.WorklogStateEnded, Stage: "committed",
					Record: api.RecordSummary{Number: 5000 + at, Kind: api.RecordKindIssue, Title: "an ended issue"},
				})
			}
			answer = ended
		default:
			http.Error(w, "every read names a state", http.StatusBadRequest)
			return
		}
	case "/api/v1/totals":
		number, _ := strconv.Atoi(r.URL.Query().Get("record"))
		totals := api.WorkTotals{Scope: api.TotalsScope{Record: &api.RecordSummary{Number: number, Kind: api.RecordKindIssue, Title: "a record"}}}
		if number == 4271 {
			totals.Scope.Record.Kind = api.RecordKindInitiative
			totals.Tokens = &api.Tokens{UncachedInput: 12863, CacheWrite: 15926436, CacheRead: 1177771241, Output: 5286566}
			totals.Estimate = &api.PreflightEstimate{Worklog: "4271.1", Tokens: api.TokenEstimate{Output: new(2396329)}}
		}
		answer = totals
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}

func waitingPreflight(day time.Time) api.Worklog {
	session := "cda42040-8d12-4c30-9d5f-f1d9325ef5af"
	waiting := api.ClaimWait{WaitingOn: "the user reading its work plan", StartedTS: day.Add(30 * time.Minute)}
	return api.Worklog{
		Name: "4271.2", RecordNumber: 4271, Sequence: 2, Step: new("preflight"), StartedTS: day, State: api.WorklogStateLive, Stage: "planned",
		Record:   api.RecordSummary{Number: 4271, Kind: api.RecordKindInitiative, Title: "Reminders fire on a date"},
		Estimate: &api.TokenEstimate{Output: new(2396329)},
		Claim: &api.Claim{
			ID: 7, RecordNumber: 4271, Holder: "claude-code/" + session, Session: &session, StartedTS: day, ExpiresTS: day.Add(24 * time.Hour),
			State: api.ClaimStateLive, Waiting: &waiting,
		},
		Waits:  []api.ClaimWait{waiting},
		Tokens: &api.Tokens{UncachedInput: 523, CacheWrite: 763000, CacheRead: 58500000, Output: 352000},
		Spans:  []api.WorklogSpan{{Session: session, StartedTS: day, Tokens: &api.Tokens{Output: 352000}}},
		Links: []api.WorklogLink{
			{Kind: api.WorklogLinkCommit, Ref: worklogsCommit, Repo: new("docs"), LinkedTS: day, LinkedBy: "someone"},
			{Kind: api.WorklogLinkPullRequest, Ref: worklogsPullRequest, Repo: new("repo"), LinkedTS: day, LinkedBy: "someone"},
			{Kind: api.WorklogLinkLanding, Ref: "ssh://host/owner/repo", Repo: new("repo"), LinkedTS: day, LinkedBy: "someone"},
		},
		DocumentVersions: []api.DocumentVersionRef{{Address: "4271/plan", Kind: "plan", Version: 1, WrittenTS: day}},
	}
}

func quietWorklog(day time.Time) api.Worklog {
	return api.Worklog{
		Name: "2103.1", RecordNumber: 2103, Sequence: 1, StartedTS: day, State: api.WorklogStateLive, Stage: "started",
		Record: api.RecordSummary{Number: 2103, Kind: api.RecordKindIssue, Title: "Pin the compost library"},
		Claim:  &api.Claim{ID: 9, RecordNumber: 2103, Holder: "slate-svc-runner", StartedTS: day, ExpiresTS: day.Add(time.Hour), State: api.ClaimStateLive},
	}
}

func (s *worklogsStandIn) lastAsked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.asked...)
}

func TestTheWorklogsTabReadsEachStateBoundedAndUnfoldsAWorklogIntoItsWork(t *testing.T) {
	opened := filepath.Join(t.TempDir(), "opened")
	bin := t.TempDir()
	for _, opener := range []string{"xdg-open", "open"} {
		if err := os.WriteFile(filepath.Join(bin, opener), []byte("#!/bin/sh\nprintf '%s' \"$1\" > "+opened+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	stand := &worklogsStandIn{}
	server := httptest.NewServer(http.HandlerFunc(stand.serve))
	t.Cleanup(server.Close)
	board := newWorklogsBoard(api.New(server.URL, server.Client()))
	model := gotermboard.NewModel(board, gotermstyle.NewPalette(false))
	model.Update(tea.WindowSizeMsg{Width: 220, Height: 60})
	settle(model, model.Init())
	ctx := context.Background()
	board.totals.Refresh(ctx)
	model.Rebuild()

	for _, want := range []string{"live 300", "expired 300", "ended 100"} {
		if !strings.Contains(strings.Join(stand.lastAsked(), ","), want) {
			t.Errorf("a read should ask for %q, asked %v", want, stand.lastAsked())
		}
	}
	screen := model.View().Content
	if strip := strings.SplitN(screen, "\n", 2)[0]; !strings.Contains(strip, "2 live · 1 expired · newest 100 of the ended") {
		t.Errorf("the strip should count each group, and name the ended read's bound: %s", strip)
	}
	if at := linesAt(tableOf(screen), []string{"live · 2", "4271.2", "2103.1", "expired · 1", "ended · newest 100"}); !isIncreasing(at) {
		t.Errorf("the live, expired and ended groups should draw in order, the live open, at lines %v:\n%s", at, screen)
	}
	if line := cursorLine(screen); !strings.Contains(line, "preflight") || !strings.Contains(line, "cda42040") || !strings.Contains(line, "the user reading its") {
		t.Errorf("the cursor should start on 4271.2, naming its step, holder and wait: %q", line)
	}
	panel := squeezed(panelOf(screen))
	for _, want := range []string{
		"waits on the user reading its work plan", "cda42040 ", "commit docs abc1234 ", "pull request repo owner/repo#40 ",
		"landing repo ssh://host/owner/repo ", "wrote 4271/plan v1",
		"this worklog 523 763.0k 58.5M 352.0k", "its estimate — — — 2.4M",
		"4271 all its work 12.9k 15.9M 1.2B 5.3M", "4271 estimate — — — 2.4M", "4271's estimate is the one preflight 4271.1 set",
	} {
		if !strings.Contains(panel, want) {
			t.Errorf("4271.2's panel should hold %q:\n%s", want, panel)
		}
	}

	model.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	board.totals.Refresh(ctx)
	model.Rebuild()
	screen = model.View().Content
	if fields := strings.Fields(strings.TrimLeft(cursorLine(screen), "▌ ▸")); len(fields) < 2 || fields[0] != "2103.1" || fields[1] != "—" {
		t.Errorf("2103.1 names no step, so its STEP should read —: %q", cursorLine(screen))
	}
	if panel := squeezed(panelOf(screen)); !strings.Contains(panel, "this worklog none reported") || !strings.Contains(panel, "2103 all its work none reported") {
		t.Errorf("a worklog with no tokens should say none reported:\n%s", panel)
	}

	model.MoveCursorTo(worklogKey("4271.2"))
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	screen = model.View().Content
	if at := linesAt(squeezed(tableOf(screen)), []string{"4271.2", "session cda42040", "commit docs abc1234", "pull request repo owner/repo#40", "landing repo"}); !isIncreasing(at) {
		t.Errorf("enter should unfold 4271.2 into its session, then its links, at lines %v:\n%s", at, screen)
	}

	model.MoveCursorTo(worklogLinkKey("4271.2", waitingPreflight(time.Now()).Links[0]))
	_, cmd := model.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	settle(model, cmd)
	if _, err := os.Stat(opened); !os.IsNotExist(err) {
		t.Errorf("o on a commit should open nothing, since a hash is no web link: %v", err)
	}
	model.MoveCursorTo(worklogLinkKey("4271.2", waitingPreflight(time.Now()).Links[1]))
	_, cmd = model.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	settle(model, cmd)
	if got, _ := os.ReadFile(opened); string(got) != worklogsPullRequest {
		t.Errorf("o on a pull request should open its link in the browser, opened %q", got)
	}

	for range 3 {
		model.Update(tea.KeyPressMsg{Code: '+', Text: "+"})
		board.ended.Refresh(ctx)
		model.Rebuild()
	}
	if asked := strings.Join(stand.lastAsked(), ","); !strings.Contains(asked, "ended 200") || !strings.Contains(asked, "ended 300") || strings.Contains(asked, "ended 400") {
		t.Errorf("+ should read 100 more ended worklogs each time, stopping at 300, asked %s", asked)
	}

	asService(t, serviceIDP(t).URL, server.URL, serviceSecret)
	out, err := runService(t, "tui", "--view", tabWorklogs, "--json")
	if err != nil {
		t.Fatal(err)
	}
	validateAgainst(t, "tui-worklogs.schema.json", out)
	var printed worklogsSnapshot
	if err := json.Unmarshal([]byte(out), &printed); err != nil {
		t.Fatal(err)
	}
	totals := printed.CursorRecordTotals
	if len(printed.Live) != 2 || len(printed.Expired) != 1 || len(printed.Ended) != endedWorklogsStep || strValue(printed.CursorWorklog) != "4271.2" ||
		totals == nil || totals.Estimate == nil || totals.Estimate.Worklog != "4271.1" {
		t.Errorf("--json should print each group and the first live worklog's record totals with its estimate:\n%s", out)
	}
}

// tableOf is the screen with the side panel cut from each line.
func tableOf(screen string) string {
	var table []string
	for line := range strings.SplitSeq(screen, "\n") {
		before, _, _ := strings.Cut(line, "│")
		table = append(table, before)
	}
	return strings.Join(table, "\n")
}

// squeezed is text with each run of spaces in a line closed to one, so a check reads no padding.
func squeezed(text string) string {
	var lines []string
	for line := range strings.SplitSeq(text, "\n") {
		lines = append(lines, strings.Join(strings.Fields(line), " ")+" ")
	}
	return strings.Join(lines, "\n")
}

// isIncreasing says every line was found, each below the one before.
func isIncreasing(at []int) bool {
	for i, line := range at {
		if line < 0 || i > 0 && line <= at[i-1] {
			return false
		}
	}
	return true
}
