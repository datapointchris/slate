package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/datapointchris/slate/cli/internal/api"
)

func TestProgressBarShowsAnyStartAndHoldsBackFullUntilNothingIsOpen(t *testing.T) {
	cases := []struct {
		done, open, filled int
	}{
		{0, 0, 0},
		{0, 4, 0},
		{1, 24, 1},
		{4, 3, 6},
		{49, 1, 9},
		{7, 0, 10},
	}
	for _, c := range cases {
		if got := progressFilled(c.done, c.open, progressCells); got != c.filled {
			t.Errorf("progressFilled(%d done, %d open) = %d, want %d", c.done, c.open, got, c.filled)
		}
	}
}

// A number read off `slate initiatives list` files an issue into that
// initiative, whether it is typed, typed with its #, or picked from the form.
func TestIssuesCreateTakesTheNumberTheInitiativeListPrints(t *testing.T) {
	validate := initiativeRef([]api.Initiative{{Number: 1501, Name: "Ship the tracker"}})
	for answer, want := range map[string]string{
		"1501":                  "1501",
		"#1501":                 "#1501",
		"1501 Ship the tracker": "1501",
		"ship the tracker":      "Ship the tracker",
	} {
		got, err := validate(answer)
		if err != nil || got != want {
			t.Errorf("initiativeRef(%q) = %q, %v; want %q", answer, got, err, want)
		}
	}
	if _, err := validate("Ship the router"); err == nil {
		t.Error("an unknown initiative name was accepted")
	}
}

// In a split pane the name keeps its floor and the repos give way, and a line
// with no room left for the name prints it whole rather than as nothing.
func TestASplitPaneKeepsTheInitiativeNameAndDropsTheRepos(t *testing.T) {
	const name = "Generate synthetic data from reviewable dataset profiles"
	const repos = "cli, datagen"
	cases := []struct {
		columns, used    int
		wantRepos        string
		nameMin, nameMax int
	}{
		{columns: 80, used: 27, wantRepos: repos, nameMin: nameFloor, nameMax: 80 - 27 - 2 - len(repos)},
		{columns: 40, used: 27, wantRepos: "", nameMin: 13, nameMax: 13},
		{columns: 40, used: 48, wantRepos: "", nameMin: utf8.RuneCountInString(name), nameMax: utf8.RuneCountInString(name)},
	}
	for _, c := range cases {
		t.Setenv("COLUMNS", strconv.Itoa(c.columns))
		gotName, gotRepos := fitNameAndRepos(name, repos, c.used)
		if n := utf8.RuneCountInString(gotName); gotRepos != c.wantRepos || n < c.nameMin || n > c.nameMax {
			t.Errorf("%d columns from %d: name %q (%d), repos %q; want %d-%d of the name and repos %q",
				c.columns, c.used, gotName, n, gotRepos, c.nameMin, c.nameMax, c.wantRepos)
		}
	}
}

// initiativeProgressAnswer is the progress the API answers for #2072: an issue
// running, one waiting, one blocked, one next, two closed, and its tokens.
const initiativeProgressAnswer = `{
  "initiative": {"id": "019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5c", "number": 2072, "name": "Move the work to slate", "status": "active",
    "priority": 2, "created_ts": "2026-10-01T00:00:00Z", "issue_count": 9, "open_count": 7, "completed_count": 1, "canceled_count": 1},
  "states": {"completed": 1, "canceled": 1, "running": 1, "waiting": 1, "ready": 1, "blocked": 1, "triage": 1, "deferred": 1,
    "shelved": 0, "open_children": 1},
  "running": [{"number": 2096, "title": "Drop the old order columns", "status": "open",
    "claim": {"holder": "claude-code/s1", "state": "live", "started_ts": "2026-11-02T09:00:00Z", "expires_ts": "2099-01-01T00:00:00Z"}}],
  "waiting": [{"number": 1812, "title": "Total the work", "status": "open",
    "claim": {"holder": "claude-code/s2", "state": "live", "started_ts": "2026-11-02T09:00:00Z", "expires_ts": "2099-01-01T00:00:00Z",
      "waiting": {"waiting_on": "a review", "started_ts": "2026-11-02T09:30:00Z"}}}],
  "blocked": [{"number": 2078, "title": "Run slate on k3s", "status": "open",
    "depends_on": [{"number": 2096, "status": "open"}, {"number": 2081, "status": "completed"}]}],
  "next": [{"kind": "issue", "number": 1789, "title": "Show initiative progress", "step": "build", "priority": 2,
    "record": {"number": 1789, "title": "Show initiative progress", "status": "open", "priority": 2, "effective_priority": 2}}],
  "closed": [{"number": 1310, "title": "List every kind in next", "status": "completed", "closed_ts": "2026-11-01T12:00:00Z"},
    {"number": 2001, "title": "Keep a second order", "status": "canceled", "closed_ts": "2026-10-30T12:00:00Z"}],
  "closed_since": "2026-10-26T00:00:00Z",
  "closed_until": null,
  "totals": {"scope": {"record": {"number": 2072, "kind": "initiative", "title": "Move the work to slate"}},
    "worklog_count": 3, "session_count": 2, "link_counts": {"commit": 4, "pull_request": 1, "landing": 1}, "message_count": 12,
    "tokens": {"uncached_input": 1200, "cache_write": 34000, "cache_read": 980000, "output": 5600},
    "tokens_outside_worklogs": null, "tokens_by_model": []}
}`

// `slate initiatives progress 2072` prints every part of the view, and its
// --json carries each of them, so fleet's summary and a TUI read one call.
func TestInitiativesProgressPrintsEveryPartAndItsJSONCarriesThem(t *testing.T) {
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		if r.URL.Path != "/api/v1/initiatives/2072/progress" {
			t.Errorf("asked %s, want the progress of #2072", r.URL.Path)
		}
		since, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("since"))
		if err != nil || time.Since(since) < 6*24*time.Hour || time.Since(since) > 8*24*time.Hour {
			t.Errorf("since=%q (%v), want the 7 days back --since defaults to", r.URL.Query().Get("since"), err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(initiativeProgressAnswer))
	}))
	t.Cleanup(server.Close)
	asService(t, serviceIDP(t).URL, server.URL, serviceSecret)

	out, err := runService(t, "initiatives", "progress", "2072")
	if err != nil {
		t.Fatalf("slate initiatives progress 2072: %v", err)
	}
	for _, want := range []string{
		"#2072  high  Move the work to slate",
		strings.Repeat("▰", 4) + strings.Repeat("▱", 26) + "  1/8 done",
		"1 done · 1 running · 1 waiting · 1 blocked · 1 ready · 1 in triage · 1 deferred · 1 with open children · 1 canceled · 9 issues",
		"Running · 1", "#2096  Drop the old order columns", "claimed by claude-code/s1 until",
		"Waiting · 1", "waiting on a review since",
		"Next · 1", "#1789  build  high  Show initiative progress",
		"Blocked · 1", "#2078  Run slate on k3s", "waits on #2096\n",
		"Closed since", "#1310  completed  2026-11-01", "#2001  canceled ",
		"3 worklogs · 2 sessions · 12 messages · 4 commits · 1 pull request · 1 landing",
		"1,200 uncached input · 34,000 cache write · 980,000 cache read · 5,600 output",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the progress view lacks %q:\n%s", want, out)
		}
	}

	out, err = runService(t, "initiatives", "progress", "2072", "--json")
	var printed []api.InitiativeProgress
	if err != nil || json.Unmarshal([]byte(out), &printed) != nil || len(printed) != 1 {
		t.Fatalf("--json printed %q, %v; want an array of one", out, err)
	}
	got := printed[0]
	if got.States.OpenChildren != 1 || len(got.Running) != 1 || len(got.Waiting) != 1 || len(got.Blocked) != 1 ||
		len(got.Next) != 1 || got.Next[0].Issue == nil || len(got.Closed) != 2 || got.ClosedSince == nil ||
		got.Totals.Tokens == nil || got.Totals.Tokens.CacheRead != 980000 || got.Totals.LinkCounts["commit"] != 4 {
		t.Errorf("--json dropped a figure the view prints: %+v", got)
	}
	if len(asked) != 2 {
		t.Errorf("asked the API %d times for two runs naming one initiative, want 2", len(asked))
	}
}
