package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/datapointchris/goclilogin"

	"github.com/datapointchris/slate/cli/internal/config"
)

const (
	serviceClientID = "slate-svc-worker"
	serviceSecret   = "service-secret"
	serviceToken    = "service-access-token"
)

// serviceIDP grants a token only to serviceClientID presenting serviceSecret in
// a Basic header and naming exactly the scopes slate asks for. Anything else gets
// the refusal an OIDC provider sends.
func serviceIDP(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": srv.URL, "token_endpoint": srv.URL + "/token"})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id, secret, ok := r.BasicAuth()
		if !ok || id != serviceClientID || secret != serviceSecret {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		if r.FormValue("grant_type") != "client_credentials" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_scope"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"` + serviceToken + `","token_type":"bearer","expires_in":300}`))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// asService points slate at idp and api as serviceClientID holding secret, with a
// state directory the test can inspect afterwards.
func asService(t *testing.T, idp, api, secret string) string {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	configure(t, api, idp, serviceClientID)
	t.Setenv(config.ClientSecretEnv, secret)
	return state
}

func runService(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestService_SearchCarriesTheClientCredentialsToken(t *testing.T) {
	var authorization string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(api.Close)
	state := asService(t, serviceIDP(t).URL, api.URL, serviceSecret)

	if _, err := runService(t, "search", "sync"); err != nil {
		t.Fatalf("search as a service: %v", err)
	}
	if authorization != "Bearer "+serviceToken {
		t.Errorf("the API saw Authorization %q, want the service token", authorization)
	}
	entries, _ := os.ReadDir(state)
	if len(entries) != 0 {
		t.Errorf("a service run wrote %v under its state directory, want nothing stored", entries)
	}
}

func TestService_ARefusedSecretNamesTheSecretRatherThanALogin(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the API was called without a token")
	}))
	t.Cleanup(api.Close)
	asService(t, serviceIDP(t).URL, api.URL, "wrong-secret")

	_, err := runService(t, "search", "sync")
	if err == nil || !strings.Contains(err.Error(), "SLATE_CLIENT_SECRET") || strings.Contains(err.Error(), "auth login") {
		t.Errorf("got %v, want the secret named and no login suggested", err)
	}
}

func TestService_StatusAsksTheProvider(t *testing.T) {
	idp := serviceIDP(t)

	asService(t, idp.URL, "http://127.0.0.1:9", serviceSecret)
	out, err := runService(t, "auth", "status", "--json")
	var report struct {
		LoggedIn bool   `json:"logged_in"`
		Type     string `json:"type"`
		Session  string `json:"session"`
	}
	if err != nil || json.Unmarshal([]byte(out), &report) != nil || !report.LoggedIn || report.Type != "service_account" || report.Session != "live" {
		t.Errorf("status with a good secret: err %v, output %s, want logged in, service_account and live", err, out)
	}

	asService(t, idp.URL, "http://127.0.0.1:9", "wrong-secret")
	out, err = runService(t, "auth", "status")
	if exitCodeFor(err) != 1 || !strings.Contains(out, "SLATE_CLIENT_SECRET") || strings.Contains(out, "auth login") {
		t.Errorf("status with a refused secret: exit %d, output %q, want 1 and the secret named", exitCodeFor(err), out)
	}
}

// A service stores no token, so with the provider down its next command fails
// too. A job gating on status has to see that.
func TestService_StatusWithTheProviderDownExitsOne(t *testing.T) {
	asService(t, "http://127.0.0.1:9", "http://127.0.0.1:9", serviceSecret)
	out, err := runService(t, "auth", "status", "--json")
	var report struct {
		LoggedIn bool   `json:"logged_in"`
		Session  string `json:"session"`
	}
	if exitCodeFor(err) != 1 || json.Unmarshal([]byte(out), &report) != nil || report.LoggedIn || report.Session != "unverified" {
		t.Errorf("status with the provider down: exit %d, output %s, want 1, not logged in, unverified", exitCodeFor(err), out)
	}
}

func TestService_LoginAndLogoutAreRefused(t *testing.T) {
	asService(t, serviceIDP(t).URL, "http://127.0.0.1:9", serviceSecret)
	for _, verb := range []string{"login", "logout"} {
		_, err := runService(t, "auth", verb)
		if err == nil || !strings.Contains(err.Error(), "SLATE_CLIENT_SECRET") {
			t.Errorf("auth %s: got %v, want it refused with the secret named", verb, err)
		}
	}
}

func TestService_ARouteOutsideTheScopeReportsTheAPIsMessage(t *testing.T) {
	message := "Access denied: Route is outside this client's scopes; they reach only GET /api/v1/issues"
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Forbidden", "message": message})
	}))
	t.Cleanup(api.Close)
	asService(t, serviceIDP(t).URL, api.URL, serviceSecret)

	_, err := runService(t, "search", "sync")
	if err == nil || !strings.Contains(err.Error(), serviceClientID) || !strings.Contains(err.Error(), message) || strings.Contains(err.Error(), "auth login") {
		t.Errorf("got %v, want the client and the API's message whole, and no login suggested", err)
	}
}

// serviceIDP grants the scopes this file spells, which is the CLI's own copy.
// So a scope the API renamed, or a route one of these calls sends that the API
// stopped listing, passes every other test here and answers 403 to every
// scheduled run.
func TestService_EachScheduledCallSendsARouteTheRequestedScopesReach(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/api/testdata/client-scopes.json")
	if err != nil {
		t.Fatalf("read the API's scope table — run tests/api/test_client_scopes.py to write it: %v", err)
	}
	var reaches map[string][]string
	if err := json.Unmarshal(raw, &reaches); err != nil {
		t.Fatalf("decode the API's scope table: %v", err)
	}

	// A {name} in these templates is a UUID, a record's number, or a worklog's
	// <number>.<sequence>. Matched as any segment, /worklogs/{name} would also
	// take /worklogs/current, which FastAPI routes to another handler. A
	// {name:path} is FastAPI's path converter, which takes any segments, and an
	// initiative is named by its name.
	reference := `(?:[0-9]+(?:\.[0-9]+)?|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`
	wildcard := regexp.MustCompile(`\{[^}]*\}`)
	scopes := config.Service(goclilogin.Deployment{}).Scopes
	var listed []*regexp.Regexp
	for _, scope := range scopes {
		for _, template := range reaches[scope] {
			var pattern strings.Builder
			last := 0
			for _, at := range wildcard.FindAllStringIndex(template, -1) {
				pattern.WriteString(regexp.QuoteMeta(template[last:at[0]]))
				if strings.HasSuffix(template[at[0]:at[1]], ":path}") {
					pattern.WriteString(".+")
				} else {
					pattern.WriteString(reference)
				}
				last = at[1]
			}
			pattern.WriteString(regexp.QuoteMeta(template[last:]))
			listed = append(listed, regexp.MustCompile("^"+pattern.String()+"$"))
		}
	}

	idp := serviceIDP(t)
	openIssue := `{"id":"019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5b","number":412,"title":"t","status":"open"}`
	heldClaim := `{"claim":{"id":1,"record_number":412,"holder":"` + serviceClientID + `","started_ts":"2026-10-19T00:00:00Z",` +
		`"expires_ts":"2026-10-20T00:00:00Z","state":"live","record":{"number":412,"kind":"issue","title":"t","status":"open"}},` +
		`"side_effects":[],"warnings":[]}`
	worklog := `{"name":"412.1","record_number":412,"sequence":1,"record":{"number":412,"kind":"issue","title":"t","status":"open"},` +
		`"opened_by":"` + serviceClientID + `","started_ts":"2026-10-19T00:00:00Z","state":"live","stage":"started"}`
	wroteWorklog := `{"worklog":` + worklog + `,"side_effects":[],"warnings":[]}`
	const fixtureSession = "0f6e2a4c-1b3d-4e5f-8a9b-0c1d2e3f4a5b"
	agentSession := `{"id":"` + fixtureSession + `","first_seen_ts":"2026-11-02T10:00:00Z","message_count":5,"tokens":{},"tokens_outside_worklogs":null,"spans":[]}`
	reportedSession := `{"session":` + agentSession + `,"side_effects":[],"warnings":[]}`
	for _, call := range []struct {
		args []string
		body string
		// byPath answers a call that makes a second request of another shape.
		byPath map[string]string
	}{
		{[]string{"search", "sync", "--json"}, `[]`, nil},
		{[]string{"issues", "list", "--json"}, `[]`, nil},
		{[]string{"issues", "list", "--blocked", "--json"}, `[]`, nil},
		{[]string{"next", "--step", "build", "--json"}, `{"queue":[],"claimed":[]}`, nil},
		{[]string{"show", "412", "--json"}, `{"kind":"idea","number":412,"record":{}}`, nil},
		{[]string{"issues", "show", "412", "--json"}, `{}`, nil},
		{[]string{"issues", "show", "019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5b", "--json"}, `{}`, nil},
		{[]string{"initiatives", "list", "--json"}, `[]`, nil},
		{
			[]string{"initiatives", "show", "Practical help guides", "--json"},
			`[]`,
			map[string]string{"/api/v1/initiatives/Practical help guides": `{}`},
		},
		{[]string{"ideas", "list", "--json"}, `[]`, nil},
		{[]string{"ideas", "show", "412", "--json"}, `{}`, nil},
		{[]string{"claims", "list", "--holder", serviceClientID, "--json"}, `[]`, nil},
		{[]string{"claims", "hold", "412", "--minutes", "2880"}, heldClaim, nil},
		{[]string{"claims", "hold", "--next"}, heldClaim, nil},
		{[]string{"claims", "extend", "412"}, heldClaim, nil},
		{[]string{"claims", "wait", "412", "--on", "the review round"}, heldClaim, nil},
		{[]string{"claims", "resume", "412"}, heldClaim, nil},
		{[]string{"claims", "release", "412"}, `{"claim":null,"side_effects":[],"warnings":[]}`, nil},
		{[]string{"worklogs", "list", "--record", "412", "--json"}, `[]`, nil},
		{[]string{"worklogs", "show", "412.1", "--json"}, worklog, nil},
		{[]string{"worklogs", "current", "--session", "run-pod-session"}, worklog, nil},
		{[]string{"worklogs", "start", "412", "--session", "run-pod-session"}, wroteWorklog, nil},
		{[]string{"worklogs", "join", "412.1", "--session", "run-pod-session"}, wroteWorklog, nil},
		{[]string{"worklogs", "link", "412.1", "--pr", "https://github.com/example/repo/pull/3"}, wroteWorklog, nil},
		{[]string{"worklogs", "edit", "412.1", "--workflow", "oneshot"}, wroteWorklog, nil},
		{[]string{"worklogs", "finish", "412.1"}, wroteWorklog, nil},
		{[]string{"sessions", "show", fixtureSession, "--json"}, agentSession, nil},
		{[]string{"sessions", "report", "--transcript", "../transcript/testdata/project/" + fixtureSession + ".jsonl"}, reportedSession, nil},
		{[]string{"comments", "add", "412", "--body", "Landed: a merged fix"}, `{"comment":{"id":1,"record_number":412}}`, nil},
		{[]string{"issues", "complete", "412"}, openIssue, nil},
		{[]string{"issues", "edit", "412", "--priority", "none", "--initiative", "Practical help guides"}, openIssue, nil},
		{[]string{"issues", "reorder", "412", "--after", "398"}, openIssue, nil},
		{[]string{"initiatives", "reorder", "Practical help guides", "--before", "398"}, `{}`, nil},
		{[]string{"ideas", "reorder", "412", "--after", "398"}, `{}`, nil},
		{
			[]string{"issues", "create", "--title", "Left of the guides", "--initiative", "Practical help guides", "--status", "triage"},
			`{}`,
			map[string]string{"/api/v1/initiatives": `[{"id":"019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5c","name":"Practical help guides"}]`},
		},
		{[]string{"initiatives", "create", "--name", "Practical help guides", "--priority", "high"}, `{}`, nil},
		{[]string{"initiatives", "drop", "Practical help guides", "--reason", "Merged into the help initiative"}, `{}`, nil},
	} {
		t.Run(strings.Join(call.args, " "), func(t *testing.T) {
			var sent []string
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent = append(sent, r.Method+" "+r.URL.Path)
				// The API refuses a scoped client any actor on a claim or
				// worklog write but its own id. A join names a session alone.
				signed := strings.HasPrefix(r.URL.Path, "/api/v1/claims") || strings.HasPrefix(r.URL.Path, "/api/v1/worklogs")
				if r.Method == http.MethodPost && signed && !strings.HasSuffix(r.URL.Path, "/join") {
					var write struct {
						Holder     string `json:"holder"`
						ReleasedBy string `json:"released_by"`
						OpenedBy   string `json:"opened_by"`
						LinkedBy   string `json:"linked_by"`
						FinishedBy string `json:"finished_by"`
					}
					err := json.NewDecoder(r.Body).Decode(&write)
					if named := write.Holder + write.ReleasedBy + write.OpenedBy + write.LinkedBy + write.FinishedBy; err != nil || named != serviceClientID {
						t.Errorf("a service wrote %s as %q (%v), want its client id %s", r.URL.Path, named, err, serviceClientID)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				body, ok := call.byPath[r.URL.Path]
				if !ok {
					body = call.body
				}
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(api.Close)
			asService(t, idp.URL, api.URL, serviceSecret)
			if _, err := runService(t, call.args...); err != nil {
				t.Fatalf("run as a service: %v", err)
			}
			if len(sent) == 0 {
				t.Fatal("sent nothing to hold against the scopes")
			}
			for _, request := range sent {
				if !slices.ContainsFunc(listed, func(re *regexp.Regexp) bool { return re.MatchString(request) }) {
					t.Errorf("sent %s, which the API lists for none of %v", request, scopes)
				}
			}
		})
	}
}

func TestHandedToken_SearchCarriesTheTokenTheCommandHands(t *testing.T) {
	var authorization string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(api.Close)
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	command := filepath.Join(root, "hand-token")
	script := "#!/bin/sh\necho '{\"access_token\": \"handed-access-token\", \"expires_in\": 300}'\n"
	if err := os.WriteFile(command, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "slate"), 0o700); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("api = %q\nclient_id = %q\ntoken_command = [%q]\n", api.URL, serviceClientID, command)
	if err := os.WriteFile(filepath.Join(root, "slate", "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := runService(t, "search", "sync"); err != nil {
		t.Fatalf("search on a handed token: %v", err)
	}
	if authorization != "Bearer handed-access-token" {
		t.Errorf("the API saw Authorization %q, want the handed token", authorization)
	}
	t.Setenv(config.ClientSecretEnv, serviceSecret)
	if _, err := runService(t, "search", "sync"); err == nil || !strings.Contains(err.Error(), "two credentials") {
		t.Errorf("a token_command beside %s: err = %v", config.ClientSecretEnv, err)
	}
}
