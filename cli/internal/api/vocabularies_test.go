package api

import (
	"encoding/json"
	"maps"
	"os"
	"slices"
	"testing"
)

// The CLI refuses a value outside its own copy before the request goes out, so
// a copy the API moved past would refuse what the API takes while both suites
// pass. The API's suite writes the file this reads.
func TestVocabularies_EachCopyHoldsWhatTheAPITakes(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/api/testdata/cli-vocabularies.json")
	if err != nil {
		t.Fatalf("read the API's vocabularies — run tests/api/test_cli_vocabularies.py to write them: %v", err)
	}
	var api map[string]json.RawMessage
	if err := json.Unmarshal(raw, &api); err != nil {
		t.Fatalf("decode the API's vocabularies: %v", err)
	}
	for name, copied := range map[string][]string{
		"claim_states":               ClaimStates,
		"worklog_states":             WorklogStates,
		"worklog_steps":              WorklogSteps,
		"worklog_link_kinds":         WorklogLinkKinds,
		"issue_statuses":             IssueStatuses,
		"issue_create_statuses":      IssueCreateStatuses,
		"issue_types":                IssueTypes,
		"priority_names":             IssuePriorityNames,
		"initiative_statuses":        InitiativeStatuses,
		"initiative_sorts":           InitiativeSorts,
		"initiative_statuses_closed": InitiativeStatusesClosed,
		"initiative_optional_steps":  InitiativeOptionalSteps,
		"pursuit_statuses":           PursuitStatuses,
		"record_kinds":               RecordKinds,
		"document_kinds":             DocumentKinds,
		"queue_steps":                QueueSteps,
	} {
		var theirs []string
		if err := json.Unmarshal(api[name], &theirs); err != nil {
			t.Errorf("decode the API's %s: %v", name, err)
		}
		if !slices.Equal(copied, theirs) {
			t.Errorf("%s = %v, the API's are %v", name, copied, theirs)
		}
	}
	var former map[string]string
	if err := json.Unmarshal(api["former_issue_statuses"], &former); err != nil {
		t.Errorf("decode the API's former_issue_statuses: %v", err)
	}
	if !maps.Equal(FormerIssueStatuses, former) {
		t.Errorf("former_issue_statuses = %v, the API's are %v", FormerIssueStatuses, former)
	}
}
