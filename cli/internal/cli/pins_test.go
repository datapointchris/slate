package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/datapointchris/slate/cli/internal/api"
)

func TestPins_EachNounSendsItsRouteAndAPinNamesTheSession(t *testing.T) {
	t.Setenv(claudeCodeSessionEnv, "2c3201ae")
	for _, c := range []struct {
		args   []string
		method string
		path   string
	}{
		{[]string{"issues", "pin", "412"}, http.MethodPost, "/issues/412/pin"},
		{[]string{"issues", "unpin", "412"}, http.MethodDelete, "/issues/412/pin"},
		{[]string{"initiatives", "pin", "1544"}, http.MethodPost, "/initiatives/1544/pin"},
		{[]string{"initiatives", "unpin", "1544"}, http.MethodDelete, "/initiatives/1544/pin"},
		{[]string{"ideas", "pin", "1530"}, http.MethodPost, "/ideas/1530/pin"},
		{[]string{"ideas", "unpin", "1530"}, http.MethodDelete, "/ideas/1530/pin"},
	} {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			var method, path string
			var sent api.PinInput
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				method, path = r.Method, r.URL.Path
				_ = json.NewDecoder(r.Body).Decode(&sent)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			t.Cleanup(server.Close)
			handToken(t, server.URL, "handed-access-token")

			if _, err := runService(t, c.args...); err != nil {
				t.Fatalf("run: %v", err)
			}
			if method != c.method || !strings.HasSuffix(path, c.path) {
				t.Errorf("sent %s %s, want %s ending %s", method, path, c.method, c.path)
			}
			if c.method == http.MethodPost && strValue(sent.Session) != "2c3201ae" {
				t.Errorf("sent session %q, want the session pinning it", strValue(sent.Session))
			}
		})
	}
}

func TestQueueRowNotes_APinnedRowSaysSoOnEveryKind(t *testing.T) {
	now := time.Now()
	for name, row := range map[string]api.QueueRow{
		"issue":      {Pinned: true, Issue: &api.Issue{Status: "open"}},
		"initiative": {Pinned: true, Initiative: &api.Initiative{}},
		"idea":       {Pinned: true, Idea: &api.Idea{}},
	} {
		if notes := queueRowNotes(row, now); !slices.Contains(notes, "pinned") {
			t.Errorf("a pinned %s reads %v, want it to say pinned", name, notes)
		}
	}
}
