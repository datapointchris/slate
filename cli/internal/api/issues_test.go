package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// A row as the API serializes it. The deferral is a bare day, which a
// time.Time field would refuse, failing every list that holds one deferred issue.
const issueRow = `{"id":"019a0000-0000-7000-8000-000000000001","number":412,"title":"Route the new paths",` +
	`"description":null,"acceptance":"curl answers 200","repo":"webapp","type":"bug","status":"open",` +
	`"status_reason":null,"priority":0,"effective_priority":2,"rank":3.5,"deferred_until_date":"2026-10-09",` +
	`"claim":null,` +
	`"initiative":{"id":"019a0000-0000-7000-8000-0000000000aa","name":"Ship the tracker","status":"active","priority":2},` +
	`"parent":null,"discovered_from":{"id":"019a0000-0000-7000-8000-000000000002","number":400,"title":"Found it","status":"completed"},` +
	`"duplicate_of":null,"labels":["area-api"],` +
	`"depends_on":[{"id":"019a0000-0000-7000-8000-000000000003","number":411,"title":"Renumber ranks","status":"open"}],` +
	`"blocks":[],"child_count":0,"open_child_count":0,"comment_count":1,"is_blocked":true,"is_ready":false,` +
	`"created_ts":"2026-10-07T21:00:00.123456Z","updated_ts":"2026-10-07T21:00:00.123456Z","closed_ts":null}`

func TestIssueDecodes_ADeferralDayAndItsEdges(t *testing.T) {
	var issue Issue
	if err := json.Unmarshal([]byte(issueRow), &issue); err != nil {
		t.Fatalf("decoding a list row: %v", err)
	}
	if issue.DeferredUntilDate == nil || *issue.DeferredUntilDate != "2026-10-09" {
		t.Errorf("deferred_until_date = %v, want the day as sent", issue.DeferredUntilDate)
	}
	if issue.EffectivePriority != 2 || issue.Initiative == nil || issue.Initiative.Priority != 2 {
		t.Errorf("effective priority %d from initiative %+v, want 2 inherited", issue.EffectivePriority, issue.Initiative)
	}
	if len(issue.DependsOn) != 1 || issue.DependsOn[0].Number != 411 || !issue.IsBlocked {
		t.Errorf("depends_on = %+v blocked=%v, want #411 and blocked", issue.DependsOn, issue.IsBlocked)
	}
}

// Priority 0 is a value someone filters for — the unprioritized pile — so a
// pointer carries it rather than a zero that reads as "no filter".
func TestListIssues_SendsAZeroPriorityAndAnEmptyRepo(t *testing.T) {
	client, query := recordQuery(t, `[]`)
	filter := IssueFilter{Repo: strptr(""), Priority: intptr(0)}
	if _, err := client.ListIssues(context.Background(), filter, Window{}, "", nil); err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if *query != "priority=0&repo=" {
		t.Errorf("query = %q, want priority=0&repo=", *query)
	}
}

// Readiness turns on whether a deferral day has arrived, so the zone travels
// with every issue read, not only with a window.
func TestListIssues_SendsTheZoneWithoutAWindow(t *testing.T) {
	client, query := recordQuery(t, `[]`)
	if _, err := client.ListIssues(context.Background(), IssueFilter{}, Window{}, "America/Chicago", nil); err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if *query != "timezone=America%2FChicago" {
		t.Errorf("query = %q, want the zone alone", *query)
	}
}

func TestGetIssue_SendsTheZone(t *testing.T) {
	client, query := recordQuery(t, issueRow)
	if _, err := client.GetIssue(context.Background(), "412", "Pacific/Kiritimati"); err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if *query != "timezone=Pacific%2FKiritimati" {
		t.Errorf("query = %q, want the zone", *query)
	}
}

// The API reads the lease and the queue filter from one flat object.
func TestHoldNextClaim_FlattensTheLeaseAndTheFilter(t *testing.T) {
	client, _, _, body := recordRequest(t, `{"claim":null,"side_effects":[],"warnings":[]}`)
	lease := ClaimLease{Holder: "session-7", Minutes: 30, Host: strptr("desk")}
	result, err := client.HoldNextClaim(context.Background(), lease, ReadyFilter{Repo: strptr(""), Label: "area-cli"}, "")
	if err != nil {
		t.Fatalf("HoldNextClaim: %v", err)
	}
	want := map[string]any{"holder": "session-7", "minutes": float64(30), "host": "desk", "repo": "", "label": "area-cli"}
	if len(*body) != len(want) {
		t.Fatalf("body = %v, want %v", *body, want)
	}
	for key, value := range want {
		if (*body)[key] != value {
			t.Errorf("body[%s] = %v, want %v", key, (*body)[key], value)
		}
	}
	if result.Claim != nil {
		t.Errorf("an empty queue decoded as %+v, want nil", result.Claim)
	}
}

func TestUpdateIssue_SendsAClearedFieldAsNull(t *testing.T) {
	client, _, _, body := recordRequest(t, issueRow)
	in := IssueUpdateInput{Title: strptr("Renamed")}
	if _, err := client.UpdateIssue(context.Background(), "412", in, []string{"deferred_until_date"}, ""); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	value, present := (*body)["deferred_until_date"]
	if !present || value != nil {
		t.Errorf("body = %v, want deferred_until_date sent as null", *body)
	}
	if len(*body) != 2 || (*body)["title"] != "Renamed" {
		t.Errorf("body = %v, want only the title and the cleared field", *body)
	}
}

func TestRemoveIssueDependency_NamesBothIssuesInThePath(t *testing.T) {
	client, method, path, _ := recordRequest(t, ``)
	if err := client.RemoveIssueDependency(context.Background(), "412", "411"); err != nil {
		t.Fatalf("RemoveIssueDependency: %v", err)
	}
	if *method != http.MethodDelete || *path != "/api/v1/issues/412/dependencies/411" {
		t.Errorf("request = %s %s", *method, *path)
	}
}
