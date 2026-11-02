package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// `slate next --json` prints the rows it decoded, so a row's record has to
// come back out under `record` as the API sent it.
func TestQueueRow_DecodesTheRecordItsKindNamesAndEncodesItBack(t *testing.T) {
	sent := `{"queue":[{"kind":"issue","number":412,"title":"t","step":"build","priority":2,"holder":null,"overlaps":[],` +
		`"record":{"number":412,"title":"t","paths":["src/api"]}}],` +
		`"claimed":[{"kind":"idea","number":7,"title":"i","step":"discuss","priority":0,"holder":"agent-a","overlaps":[],` +
		`"record":{"number":7,"title":"i"}}]}`
	var queue Queue
	if err := json.Unmarshal([]byte(sent), &queue); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if issue := queue.Queue[0].Issue; issue == nil || issue.Paths[0] != "src/api" {
		t.Fatalf("queue[0].Issue = %+v, want the issue with its paths", issue)
	}
	if idea := queue.Claimed[0].Idea; idea == nil || idea.Number != 7 {
		t.Fatalf("claimed[0].Idea = %+v, want idea #7", idea)
	}
	printed, err := json.Marshal(queue)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(string(printed), `"record":{"id":"","number":412,"title":"t"`) {
		t.Errorf("encoded %s, want the issue under record", printed)
	}
}
