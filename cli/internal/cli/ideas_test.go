package cli

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/datapointchris/slate/cli/internal/api"
)

// A script runs `slate ideas promote --json` and reads the promoted number from
// the answer's `initiative` or `issue`. Each recorded answer is the API's own,
// from promoting a real idea on a copy of slate's data, with its text scrubbed.
// An answer holding no initiative fails rather than naming #0.
func TestIdeasPromote_SendsOnlyItsKindsFieldsAndReadsTheNumberFromTheAnswer(t *testing.T) {
	cases := []struct {
		name     string
		recorded string
		dropped  string
		args     []string
		sent     map[string]any
		numbers  map[string]float64
	}{
		{
			"an initiative",
			"promote-initiative.json",
			"",
			[]string{"ideas", "promote", "4224", "--summary", "It runs.", "--json"},
			map[string]any{"kind": "initiative", "summary": "It runs."},
			map[string]float64{"initiative": 4224},
		},
		{
			"an issue",
			"promote-issue.json",
			"",
			[]string{"ideas", "promote", "4245", "--to", "issue", "--repo", "docs", "--path", "docs", "--type", "task", "--acceptance", "It reads right.", "--json"},
			map[string]any{"kind": "issue", "repo": "docs", "paths": []any{"docs"}, "type": "task", "acceptance": "It reads right."},
			map[string]float64{"issue": 4245},
		},
		{
			"an answer holding no initiative",
			"promote-initiative.json",
			"initiative",
			[]string{"ideas", "promote", "4224", "--summary", "It runs.", "--json"},
			map[string]any{"kind": "initiative", "summary": "It runs."},
			nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "recorded", c.recorded))
			if err != nil {
				t.Fatal(err)
			}
			var answer map[string]any
			if err := json.Unmarshal(raw, &answer); err != nil {
				t.Fatal(err)
			}
			delete(answer, c.dropped)
			body, _ := json.Marshal(answer)
			var path string
			var sent map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				request, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(request, &sent)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			}))
			t.Cleanup(server.Close)
			handToken(t, server.URL, "handed-access-token")

			out, err := runService(t, c.args...)
			if want := "/api/v1/ideas/" + c.args[2] + "/promote"; path != want {
				t.Errorf("sent to %s, want %s", path, want)
			}
			if !reflect.DeepEqual(sent, c.sent) {
				t.Errorf("sent %v, want %v", sent, c.sent)
			}
			if c.numbers == nil {
				if err == nil {
					t.Fatalf("an answer holding no initiative succeeded, printing %s", out)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var printed map[string]any
			if err := json.Unmarshal([]byte(out), &printed); err != nil {
				t.Fatalf("decode %s: %v", out, err)
			}
			for member, want := range c.numbers {
				record, _ := printed[member].(map[string]any)
				got := record["number"]
				if got != want {
					t.Errorf("%s names #%v, want #%v", member, got, want)
				}
			}
		})
	}
}

// `slate ideas fold` names the idea, the record it went into and every row that
// moved, from the API's own answers to folding a real idea on a copy of
// slate's data. A refusal prints the API's sentence, and an API from before
// the fold says it has none rather than that the idea is missing.
func TestIdeasFold_NamesWhatMovedOrWhatRefusedIt(t *testing.T) {
	recorded := func(name string) string {
		raw, err := os.ReadFile(filepath.Join("testdata", "recorded", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	cases := []struct {
		name   string
		status int
		body   string
		out    []string
		err    string
	}{
		{
			"into a closed initiative", http.StatusOK, recorded("fold-into-closed-initiative.json"),
			[]string{
				"Folded idea #4086 \"Neutral title 1\" into initiative #2356 \"Neutral name 1\", which is completed.\n",
				"  Moved worklog 4086.1 to #2356 as 2356.6.\n  Moved 1 claim to #2356.\n  Moved 1 comment to #2356.\n  #4086 now leads to #2356.\n",
			},
			"",
		},
		{
			"refused for another session's claim", http.StatusConflict, recorded("fold-refused-for-claim.json"), nil,
			"claude-code/copy-check-second holds #2926 until 2026-11-25 05:19 UTC; it ends or releases its claim first",
		},
		{
			"by an API from before the fold", http.StatusNotFound, `{"error":"Not Found","message":"no route answers /api/v1/ideas/4086/fold"}`, nil,
			"this API has no fold",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var sent map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"id":"019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5d","number":4086,"title":"Neutral title 1"}`))
					return
				}
				request, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(request, &sent)
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			t.Cleanup(server.Close)
			handToken(t, server.URL, "handed-access-token")

			out, err := runTreeOut(t, "ideas", "fold", "4086", "--into", "2356", "--holder", "claude-code/s")
			if want := map[string]any{"into": "2356", "holder": "claude-code/s"}; !reflect.DeepEqual(sent, want) {
				t.Errorf("sent %v, want %v", sent, want)
			}
			if c.err != "" {
				if err == nil || !strings.HasPrefix(err.Error(), c.err) || exitCodeFor(err) != 1 {
					t.Errorf("fold = %v, exit %d; want %q, exit 1", err, exitCodeFor(err), c.err)
				}
				return
			}
			for _, want := range c.out {
				if err != nil || !strings.Contains(out, want) {
					t.Errorf("fold printed %q, %v; want %q", out, err, want)
				}
			}
		})
	}
}

// A field the API adds to a promotion's kind reaches the CLI through the
// vocabularies document, and fails here until a flag sets it.
func TestIdeasPromote_EachFieldOfAPromotionHasAFlag(t *testing.T) {
	fields := slices.Concat(api.PromoteSharedFields, api.PromoteFieldsOfKind[api.PromotedToInitiative], api.PromoteFieldsOfKind[api.PromotedToIssue])
	slices.Sort(fields)
	flagged := slices.Sorted(maps.Values(promoteFieldOfFlag))
	if !slices.Equal(fields, flagged) {
		t.Errorf("the flags set %v, a promotion takes %v", flagged, fields)
	}
	flags := newIdeasPromoteCommand().Flags()
	for flag := range promoteFieldOfFlag {
		if flags.Lookup(flag) == nil {
			t.Errorf("--%s sets a field, and the command has no such flag", flag)
		}
	}
}
