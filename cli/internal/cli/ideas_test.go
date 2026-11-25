package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Each recorded answer is the API's own, from promoting a real idea on a copy
// of slate's data, scrubbed of its text. A promotion keeps the idea's number,
// so each line names it once.
func TestIdeasPromote_SendsOnlyItsKindsFieldsAndPrintsTheRecordedAnswer(t *testing.T) {
	ended := func(number string) string {
		return "  Ended claude-code/13cb8a46-copy-check's claim on #" + number + ", because #" + number + " is promoted.\n" +
			"  Ended worklog " + number + ".1 as promoted.\n"
	}
	cases := []struct {
		recorded string
		args     []string
		sent     map[string]any
		printed  string
		warned   string
	}{
		{
			"promote-initiative.json",
			[]string{"ideas", "promote", "4224", "--summary", "It runs."},
			map[string]any{"summary": "It runs."},
			"Promoted #4224 to an initiative, under the same number.\n" + ended("4224"),
			"",
		},
		{
			"promote-issue.json",
			[]string{"ideas", "promote", "4245", "--to", "issue", "--repo", "docs", "--path", "docs", "--type", "task", "--acceptance", "It reads right."},
			map[string]any{"kind": "issue", "repo": "docs", "paths": []any{"docs"}, "type": "task", "acceptance": "It reads right."},
			"Promoted #4245 to an issue in docs, under the same number.\n" + ended("4245"),
			"Warning: Dropped pursuit #2991 Neutral name 1, because an issue takes its initiative's pursuits.\n",
		},
	}
	for _, c := range cases {
		t.Run(c.recorded, func(t *testing.T) {
			answer, err := os.ReadFile(filepath.Join("testdata", "recorded", c.recorded))
			if err != nil {
				t.Fatal(err)
			}
			var path string
			var sent map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &sent)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(answer)
			}))
			t.Cleanup(server.Close)
			handToken(t, server.URL, "handed-access-token")

			root := NewRootCommand()
			var printed, warned bytes.Buffer
			root.SetOut(&printed)
			root.SetErr(&warned)
			root.SetArgs(c.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("slate %s: %v", strings.Join(c.args, " "), err)
			}
			if want := "/api/v1/ideas/" + c.args[2] + "/promote"; path != want {
				t.Errorf("sent to %s, want %s", path, want)
			}
			if !reflect.DeepEqual(sent, c.sent) {
				t.Errorf("sent %v, want %v", sent, c.sent)
			}
			if printed.String() != c.printed || warned.String() != c.warned {
				t.Errorf("printed\n%s\nand warned\n%s\nwant\n%s\nand\n%s", printed.String(), warned.String(), c.printed, c.warned)
			}
		})
	}
}
