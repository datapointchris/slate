package api

import (
	"encoding/json"
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
	var api map[string][]string
	if err := json.Unmarshal(raw, &api); err != nil {
		t.Fatalf("decode the API's vocabularies: %v", err)
	}
	for name, copied := range map[string][]string{
		"claim_states":               ClaimStates,
		"worklog_states":             WorklogStates,
		"worklog_workflows":          WorklogWorkflows,
		"worklog_link_kinds":         WorklogLinkKinds,
		"issue_statuses":             IssueStatuses,
		"issue_types":                IssueTypes,
		"priority_names":             IssuePriorityNames,
		"initiative_statuses":        InitiativeStatuses,
		"initiative_sorts":           InitiativeSorts,
		"initiative_statuses_closed": InitiativeStatusesClosed,
		"record_kinds":               RecordKinds,
		"document_kinds":             DocumentKinds,
		"queue_steps":                QueueSteps,
	} {
		if !slices.Equal(copied, api[name]) {
			t.Errorf("%s = %v, the API's are %v", name, copied, api[name])
		}
	}
}
