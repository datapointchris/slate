package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/api"
	"github.com/datapointchris/slate/cli/internal/prompt"
)

// Every refusal that needs no server state is decided before the client
// exists, so it exits 2 even with the API unreachable.
func TestIssues_AUsageRefusalDoesNotWaitOnTheNetwork(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"a queue flag beside a named record", []string{"claims", "hold", "412", "--repo", "webapp"}},
		{"a hold naming a record and --next", []string{"claims", "hold", "412", "--next"}},
		{"a hold naming neither", []string{"claims", "hold"}},
		{"a claim shorter than a minute", []string{"claims", "hold", "--next", "--minutes", "0"}},
		{"a claim with no holder", []string{"claims", "hold", "--next", "--holder", ""}},
		{"an unknown claim state", []string{"claims", "list", "--state", "held"}},
		{"an idea edit changing nothing", []string{"ideas", "edit", "1860"}},
		{"a promotion into a closed status", []string{"ideas", "promote", "1860", "--status", "completed"}},
		{"a cancel with no reason", []string{"issues", "cancel", "412"}},
		{"a comment given twice", []string{"issues", "add-comment", "412", "shipped", "--body", "shipped"}},
		{"an edit changing nothing", []string{"issues", "edit", "412"}},
		{"emptying the title", []string{"issues", "edit", "412", "--title", ""}},
		{"a label beside an empty one", []string{"issues", "edit", "412", "--label", "area-api", "--label", ""}},
		{"a deferral that is not a day", []string{"issues", "edit", "412", "--deferred-until", "tomorrow"}},
		{"an unknown type", []string{"issues", "edit", "412", "--type", "epic"}},
		{"a title missing with no terminal", []string{"issues", "create", "--no-input"}},
		{"a new issue already closed", []string{"issues", "create", "--title", "x", "--status", "completed"}},
		{"an unknown status", []string{"issues", "list", "--status", "done"}},
		{"an unknown priority", []string{"issues", "list", "--priority", "extreme"}},
		{"a reorder with no neighbor", []string{"issues", "reorder", "412"}},
		{"a reorder with both neighbors", []string{"issues", "reorder", "412", "--before", "1", "--after", "2"}},
		{"a negative tree depth", []string{"dependencies", "tree", "--depth", "-1"}},
		{"an initiative dropped with no reason", []string{"initiatives", "drop", "Ship it"}},
		{"a label edit changing nothing", []string{"labels", "edit", "area-api"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// An unknown verb exits 2 as well, so a row naming one would pass.
			if found, _, err := NewRootCommand().Find(tc.args); err != nil || found.HasSubCommands() {
				t.Fatalf("%v reaches no command: %v", tc.args, err)
			}
			unreachableAPI(t)
			if code := runTree(t, tc.args...); code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
		})
	}
}

func parsedEdit(t *testing.T, args ...string) (*issueEditFlags, *cobra.Command) {
	t.Helper()
	v := &issueEditFlags{}
	cmd := &cobra.Command{}
	v.register(cmd)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags(%v): %v", args, err)
	}
	return v, cmd
}

// An empty --deferred-until or --initiative goes out as a null. An empty
// --label goes out as an empty list, because the API answers a null labels
// with 422.
func TestIssueEdit_EmptyValuesClearEachFieldItsOwnWay(t *testing.T) {
	v, cmd := parsedEdit(t, "--deferred-until", "", "--initiative", "", "--label", "", "--priority", "none")
	in, clear, err := v.update(cmd)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if strings.Join(clear, ",") != "deferred_until_date,initiative" {
		t.Errorf("clear = %v, want the deferral and the initiative", clear)
	}
	if in.Labels == nil || len(*in.Labels) != 0 {
		t.Errorf("labels = %v, want an empty list", in.Labels)
	}
	if in.Priority == nil || *in.Priority != 0 {
		t.Errorf("priority = %v, want 0 for none", in.Priority)
	}
	if in.DeferredUntilDate != nil || in.Initiative != nil {
		t.Errorf("a cleared field also went in the body: %+v", in)
	}
}

// Two sessions on one machine must not share a holder, because a hold under a
// name that already holds the record goes through as already held. The prefix is
// the kind of actor, which is what a reader filters a person's work from a
// worker's by.
func TestActorIdentity_NamesTheKindOfActorBeforeWhoItIs(t *testing.T) {
	cases := []struct {
		name, session, attended, worker, want string
	}{
		{"a session someone attends", "2c3201ae", "1", "", "claude-code/2c3201ae"},
		{"an older session that does not say", "2c3201ae", "", "", "claude-code/2c3201ae"},
		{"a session nobody attends", "2c3201ae", "0", "", "claude-code-unattended/2c3201ae"},
		{"a session a named worker started", "2c3201ae", "0", "fix@scheduler", "claude-code-unattended/fix@scheduler/2c3201ae"},
		{"a named worker running slate itself", "", "", "runner-update@scheduler", "worker/runner-update@scheduler"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CODE_SESSION_ID", c.session)
			t.Setenv("CLAUDE_CODE_SESSION_ATTENDED", c.attended)
			t.Setenv(actorWorkerEnv, c.worker)
			if got := actorIdentity(); got != c.want {
				t.Errorf("actorIdentity() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestActorIdentity_IsThePersonOutsideASessionOrAWorker(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv(actorWorkerEnv, "")
	t.Setenv("USER", "chris")
	if got := actorIdentity(); !strings.HasPrefix(got, "chris") {
		t.Errorf("actorIdentity() = %q outside a session, want the user", got)
	}
}

// A Claude Code session closing an issue: the PATCH names the session unless
// --closed-by names someone else or is emptied.
func TestIssues_CompleteAndCancelSendWhoIsClosingInTheirPatch(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "2c3201ae")
	t.Setenv("CLAUDE_CODE_SESSION_ATTENDED", "1")
	t.Setenv(actorWorkerEnv, "")
	idp := serviceIDP(t)
	verbs := [][]string{{"issues", "complete", "412"}, {"issues", "cancel", "412", "--reason", "superseded"}}
	cases := []struct {
		flags []string
		want  *string
	}{
		{nil, ptr("claude-code/2c3201ae")},
		{[]string{"--closed-by", "chris@desk"}, ptr("chris@desk")},
		{[]string{"--closed-by", ""}, nil},
	}
	for _, verb := range verbs {
		for _, c := range cases {
			args := append(slices.Clone(verb), c.flags...)
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				var patch map[string]any
				api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPatch {
						_ = json.NewDecoder(r.Body).Decode(&patch)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5b","number":412,"title":"t","status":"open"}`))
				}))
				t.Cleanup(api.Close)
				asService(t, idp.URL, api.URL, serviceSecret)

				if _, err := runService(t, args...); err != nil {
					t.Fatalf("run: %v", err)
				}
				if patch == nil {
					t.Fatal("sent no PATCH")
				}
				got, sent := patch["closed_by"]
				switch {
				case c.want == nil && sent:
					t.Errorf("sent closed_by %v, want it left out", got)
				case c.want != nil && got != *c.want:
					t.Errorf("sent closed_by %v, want %q", got, *c.want)
				}
			})
		}
	}
}

func TestIssues_AddCommentTakesTheTextAfterTheIssue(t *testing.T) {
	var comment struct {
		Body string `json:"body"`
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&comment)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(api.Close)
	asService(t, serviceIDP(t).URL, api.URL, serviceSecret)

	if _, err := runService(t, "issues", "add-comment", "412", "Shipped in 55a0881"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if comment.Body != "Shipped in 55a0881" {
		t.Errorf("sent body %q, want the text after the issue", comment.Body)
	}
}

// Reading what attended sessions finished: --closed-by reaches the API as the
// prefix it matches on.
func TestIssues_ListClosedBySendsThePrefixToMatch(t *testing.T) {
	var query url.Values
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(api.Close)
	asService(t, serviceIDP(t).URL, api.URL, serviceSecret)

	if _, err := runService(t, "issues", "list", "--status", "completed", "--closed-by", "claude-code/", "--json"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := query.Get("closed_by"); got != "claude-code/" {
		t.Errorf("sent closed_by %q, want claude-code/", got)
	}
}

func TestClosedNote_SaysWhenAndWhoWhereEachIsKnown(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local)
	closed := now.Add(-time.Hour)
	who := "claude-code/2c3201ae"
	cases := []struct {
		name  string
		issue api.Issue
		want  string
	}{
		{"open", api.Issue{}, ""},
		{"closed by a known actor", api.Issue{ClosedTS: &closed, ClosedBy: &who}, "11:00 by claude-code/2c3201ae"},
		{"closed before anyone was recorded", api.Issue{ClosedTS: &closed}, "11:00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := closedNote(c.issue, now); got != c.want {
				t.Errorf("closedNote() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestIssueStateNotes_SayWhyAnIssueWaits(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local)
	later := now.Add(2 * time.Hour)
	tomorrow := now.AddDate(0, 0, 1).Format(dayLayout)
	yesterday := now.AddDate(0, 0, -1).Format(dayLayout)
	issue := api.Issue{
		Status:            api.IssueStatusOpen,
		Claim:             &api.Claim{Holder: "claude-code/abc", ExpiresTS: later, State: api.ClaimStateLive},
		DeferredUntilDate: &tomorrow,
		DependsOn: []api.IssueSummary{
			{Number: 411, Status: api.IssueStatusOpen},
			{Number: 400, Status: api.IssueStatusCompleted},
		},
		OpenChildCount: 1,
	}
	want := "claimed by claude-code/abc until 14:00; blocked by #411; deferred until " + tomorrow + "; 1 open child"
	if got := strings.Join(issueStateNotes(issue, now), "; "); got != want {
		t.Errorf("notes = %q\nwant    %q", got, want)
	}

	issue.DeferredUntilDate = &yesterday
	issue.Claim.State = api.ClaimStateExpired
	issue.DependsOn = nil
	issue.OpenChildCount = 0
	if got := strings.Join(issueStateNotes(issue, now), "; "); got != "claim by claude-code/abc expired" {
		t.Errorf("notes = %q, want only the expired claim", got)
	}

	issue.Status = api.IssueStatusCompleted
	if notes := issueStateNotes(issue, now); len(notes) != 0 {
		t.Errorf("a closed issue carried notes %v", notes)
	}
}

func TestPriorityCell_StarsAnInheritedPriority(t *testing.T) {
	if got := priorityCell(api.Issue{Priority: 0, EffectivePriority: 2}); got != "high*" {
		t.Errorf("inherited = %q, want high*", got)
	}
	if got := priorityCell(api.Issue{Priority: 0, EffectivePriority: 0}); got != "none" {
		t.Errorf("none = %q, want none", got)
	}
}

// A star on screen with its meaning only in another command's --help is a
// symbol the reader has to go and look up.
func TestNoteInheritedPriority_ExplainsTheStarWhenARowCarriesOne(t *testing.T) {
	var stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&stderr)

	noteInheritedPriority(cmd, []api.Issue{{Priority: 2, EffectivePriority: 2}, {Priority: 0, EffectivePriority: 2}})

	if !strings.Contains(stderr.String(), "* priority inherited from what the issue blocks, its parent, or its initiative") {
		t.Errorf("note = %q, want the star explained", stderr.String())
	}
}

func TestNoteInheritedPriority_SilentWhenNoRowIsStarred(t *testing.T) {
	var stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&stderr)

	noteInheritedPriority(cmd, []api.Issue{{Priority: 2, EffectivePriority: 2}})

	if stderr.String() != "" {
		t.Errorf("note = %q, want silence with no star on screen", stderr.String())
	}
}

func TestReinvocation_KeepsTheFiltersItWidens(t *testing.T) {
	cmd := findCommand(t, "issues", "list")
	if err := cmd.ParseFlags([]string{"--repo", "", "--blocked=false", "--label", "area-cli", "--json"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	want := "slate issues list --blocked=false --label area-cli --repo '' --status all"
	if got := reinvocation(cmd, "--status all"); got != want {
		t.Errorf("reinvocation = %q\nwant          %q", got, want)
	}
}

func TestResolveIssueComment_ByPlaceOrById(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":"019a0000-0000-7000-8000-00000000000a","issue_id":"i","body":"first","created_ts":"2026-10-07T12:00:00Z"},
			{"id":"019a0000-0000-7000-8000-00000000000b","issue_id":"i","body":"second","created_ts":"2026-10-07T12:00:00Z"}
		]`))
	}))
	t.Cleanup(srv.Close)
	client := api.New(srv.URL, srv.Client())
	ctx := context.Background()

	for ref, want := range map[string]string{"2": "second", "#1": "first", "019a0000-0000-7000-8000-00000000000b": "second"} {
		comment, err := resolveIssueComment(ctx, client, "412", ref)
		if err != nil || comment.Body != want {
			t.Errorf("resolve %q = %q, %v; want %q", ref, comment.Body, err, want)
		}
	}
	if _, err := resolveIssueComment(ctx, client, "412", "3"); err == nil || !strings.Contains(err.Error(), "no #3") {
		t.Errorf("resolve 3 = %v, want it to say there is no #3", err)
	}
}

func TestIssueCreateFields_OffersEveryInitiativeThatTakesIssues(t *testing.T) {
	t.Setenv("SLATE_REPOS_REGISTRY", filepath.Join(t.TempDir(), "repos.json"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/initiatives" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if got := r.URL.Query().Get("status"); got != "all" {
			t.Errorf("listed initiatives with status %q, want all", got)
		}
		_, _ = w.Write([]byte(`[
			{"id":"a","number":1,"name":"Ship it","status":"active"},
			{"id":"b","number":2,"name":"Parked idea","status":"someday"},
			{"id":"d","number":4,"name":"Done work","status":"completed"},
			{"id":"e","number":5,"name":"Abandoned","status":"dropped"}
		]`))
	}))
	t.Cleanup(srv.Close)

	fields, err := issueCreateFields(context.Background(), api.New(srv.URL, srv.Client()))
	if err != nil {
		t.Fatalf("issueCreateFields: %v", err)
	}
	field := fields[slices.IndexFunc(fields, func(f prompt.Field) bool { return f.Key == "initiative" })]
	if want := []string{"1 Ship it", "2 Parked idea"}; !slices.Equal(field.Choices, want) {
		t.Errorf("initiative choices = %v, want %v", field.Choices, want)
	}
	if got, err := field.Validate("parked idea"); err != nil || got != "Parked idea" {
		t.Errorf("a someday initiative's name = %q, %v, want it accepted", got, err)
	}
	if _, err := field.Validate("Done work"); err == nil {
		t.Error("a completed initiative's name was accepted, want it refused")
	}
}

func TestIssueTrees_HideFinishedWorkByDefault(t *testing.T) {
	byID := map[string]api.Issue{
		"a": {ID: "a", Status: api.IssueStatusCompleted},
		"b": {ID: "b", Status: api.IssueStatusCanceled},
		"c": {ID: "c", Status: api.IssueStatusOpen},
	}
	if issueComponentMatches([]string{"a", "b"}, byID, "", nil) {
		t.Error("a finished tree was kept by default")
	}
	if !issueComponentMatches([]string{"a", "c"}, byID, "", nil) {
		t.Error("a tree with open work was hidden")
	}
	if !issueComponentMatches([]string{"a", "b"}, byID, api.IssueStatusAll, nil) {
		t.Error("--status all hid a finished tree")
	}
}
