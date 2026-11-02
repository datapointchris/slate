package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/datapointchris/slate/cli/internal/api"
)

func TestComments_AddSendsTheTextAfterTheRecordAndTheSessionWritingIt(t *testing.T) {
	var sent api.CommentAddInput
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"comment":{"id":7,"record_number":412,"worklog":"412.2"}}`))
	}))
	t.Cleanup(server.Close)
	asService(t, serviceIDP(t).URL, server.URL, serviceSecret)
	t.Setenv(claudeCodeSessionEnv, "s1")

	out, err := runService(t, "comments", "add", "412", "--document", "plan", "--line", "2", "Split this step")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if sent.Body != "Split this step" || strValue(sent.Session) != "s1" || strValue(sent.Document) != "plan" || sent.Line == nil || *sent.Line != 2 {
		t.Errorf("sent %+v, want the text after the record on plan line 2 from session s1", sent)
	}
	if !strings.Contains(out, "in worklog 412.2") {
		t.Errorf("printed %q, want the worklog the comment names", out)
	}
}

func TestDocumentsShow_PrintsALineCommentUnderItsLineAndOneWhoseLineChangedAfter(t *testing.T) {
	two, now := 2, time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	comment := func(id int, line int, text string, shown *int) api.PlacedComment {
		return api.PlacedComment{
			Comment:   api.Comment{ID: id, RecordNumber: 412, Body: "comment " + text, WrittenTS: now, On: &api.CommentLine{Document: "plan", Version: 1, Line: line, Text: text}},
			ShownLine: shown,
		}
	}
	document := api.Document{
		Record:  api.RecordSummary{Number: 412, Title: "Retry the upload"},
		Address: "412/plan",
		Kind:    "plan",
		Version: api.DocumentVersion{
			DocumentVersionSummary: api.DocumentVersionSummary{Version: 2, WrittenBy: "s", WrittenTS: now, LineCount: 3},
			Body:                   "a\nb\nc moved\n",
			Lines:                  []string{"a", "b", "c moved"},
		},
		Versions: []api.DocumentVersionSummary{{Version: 1}, {Version: 2}},
		Comments: []api.PlacedComment{comment(1, 2, "b", &two), comment(2, 3, "c", nil)},
	}
	var out bytes.Buffer
	printDocument(&out, document, now)

	printed := out.String()
	under := strings.Index(printed, "comment b")
	if line2, line3 := strings.Index(printed, "  2  b"), strings.Index(printed, "  3  c moved"); line2 < 0 || under < line2 || under > line3 {
		t.Errorf("want the comment on b printed between lines 2 and 3:\n%s", printed)
	}
	if changed := strings.Index(printed, "Comments on lines not in version 2 (1)"); changed < strings.Index(printed, "  3  c moved") ||
		!strings.Contains(printed[changed:], "it was on: c") {
		t.Errorf("want the comment on c after the document, quoting its line:\n%s", printed)
	}
}
