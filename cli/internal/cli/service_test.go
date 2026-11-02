package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/datapointchris/slate/cli/internal/config"
)

const (
	serviceClientID = "slate-svc-worker"
	serviceSecret   = "service-secret"
)

// apiScopeTable is the API's routes by scope, which tests/api/test_client_scopes.py writes.
func apiScopeTable(t *testing.T) map[string][]string {
	t.Helper()
	raw, err := os.ReadFile("../../../tests/api/testdata/client-scopes.json")
	if err != nil {
		t.Fatalf("read the API's scope table — run tests/api/test_client_scopes.py to write it: %v", err)
	}
	var reaches map[string][]string
	if err := json.Unmarshal(raw, &reaches); err != nil {
		t.Fatalf("decode the API's scope table: %v", err)
	}
	return reaches
}

// serviceIDP is scopedIDP for a client registered for every scope the API lists.
func serviceIDP(t *testing.T) *httptest.Server {
	t.Helper()
	return scopedIDP(t, false, slices.Collect(maps.Keys(apiScopeTable(t)))...)
}

// scopedIDP grants serviceClientID, presenting serviceSecret in a Basic header,
// a token for the scopes it asks that held lists. A request naming any other is
// refused with invalid_scope, as Authelia refuses it. With narrows the provider
// grants the held part instead, as RFC 6749 §3.3 allows. Either way the
// response names what it granted, and the token is that list.
func scopedIDP(t *testing.T, narrows bool, held ...string) *httptest.Server {
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
			_, _ = w.Write([]byte(`{"error":"unsupported_grant_type"}`))
			return
		}
		granted := []string{}
		for _, scope := range strings.Fields(r.FormValue("scope")) {
			switch {
			case slices.Contains(held, scope):
				granted = append(granted, scope)
			case !narrows:
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "invalid_scope", "error_description": "The OAuth 2.0 Client is not allowed to request scope '" + scope + "'.",
				})
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": tokenFor(granted), "token_type": "bearer", "expires_in": 300, "scope": strings.Join(granted, " "),
		})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// tokenFor is the access token scopedIDP issues for scopes, which carries them
// so a fake API can gate on them as the real one does.
func tokenFor(scopes []string) string { return "scopes:" + strings.Join(scopes, ",") }

// tokenScopes reads back the scopes an Authorization header's token carries.
func tokenScopes(authorization string) []string {
	carried, _ := strings.CutPrefix(authorization, "Bearer scopes:")
	return strings.FieldsFunc(carried, func(r rune) bool { return r == ',' })
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

// handToken points slate at api with a token_command handing token, the
// credential that asks no grant of slate's own.
func handToken(t *testing.T, api, token string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	command := filepath.Join(root, "hand-token")
	script := fmt.Sprintf("#!/bin/sh\necho '{\"access_token\": %q, \"expires_in\": 300}'\n", token)
	if err := os.WriteFile(command, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "slate"), 0o700); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("api = %q\nclient_id = %q\ntoken_command = [%q]\n", api, serviceClientID, command)
	if err := os.WriteFile(filepath.Join(root, "slate", "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
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

func TestService_SearchCarriesATokenForItsOwnScopeAndStoresNothing(t *testing.T) {
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
	if want := "Bearer " + tokenFor([]string{scopeRecordsRead}); authorization != want {
		t.Errorf("the API saw Authorization %q, want %q", authorization, want)
	}
	entries, _ := os.ReadDir(state)
	if len(entries) != 0 {
		t.Errorf("a service run wrote %v under its state directory, want nothing stored", entries)
	}
}

// Each refusal names what fixes it, and none suggests a login a service cannot make.
func TestService_ARefusalNamesWhatFixesIt(t *testing.T) {
	for _, refusal := range []struct {
		name    string
		narrows bool
		held    []string
		secret  string
		args    []string
		want    []string
	}{
		{"a wrong secret", false, serviceScopes, "wrong-secret", []string{"search", "sync"}, []string{"SLATE_CLIENT_SECRET"}},
		{
			"a scope the client lacks", false,
			[]string{scopeRecordsRead},
			serviceSecret,
			[]string{"issues", "complete", "412"},
			[]string{serviceClientID, scopeIssuesClose, "slate issues complete", "not allowed to request scope"},
		},
		{
			"a grant narrowed past the command's scopes", true,
			[]string{scopeRecordsRead},
			serviceSecret,
			[]string{"issues", "complete", "412"},
			[]string{serviceClientID, scopeIssuesClose, "slate issues complete"},
		},
		{"a command no scope reaches", false, serviceScopes, serviceSecret, []string{"labels", "list"}, []string{"only a person", "slate labels list"}},
	} {
		t.Run(refusal.name, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("the API was called with %s", r.Header.Get("Authorization"))
			}))
			t.Cleanup(api.Close)
			asService(t, scopedIDP(t, refusal.narrows, refusal.held...).URL, api.URL, refusal.secret)

			_, err := runService(t, refusal.args...)
			if err == nil || strings.Contains(err.Error(), "auth login") {
				t.Fatalf("got %v, want a refusal suggesting no login", err)
			}
			for _, want := range refusal.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("got %q, want it to name %q", err, want)
				}
			}
		})
	}
}

// A service client holds only the scopes its commands need, so status asks for
// each alone, and auth token asks for all it holds.
func TestService_StatusListsTheScopesTheClientHolds(t *testing.T) {
	held := []string{scopeRecordsRead, scopeClaimsHold}
	for _, provider := range []struct {
		name    string
		narrows bool
	}{{"refusing an unheld scope", false}, {"narrowing to the held scopes", true}} {
		t.Run(provider.name, func(t *testing.T) {
			asService(t, scopedIDP(t, provider.narrows, held...).URL, "http://127.0.0.1:9", serviceSecret)
			out, err := runService(t, "auth", "status", "--json")
			var report struct {
				LoggedIn bool     `json:"logged_in"`
				Type     string   `json:"type"`
				Session  string   `json:"session"`
				Scopes   []string `json:"scopes"`
			}
			if err != nil || json.Unmarshal([]byte(out), &report) != nil || !report.LoggedIn || report.Type != "service_account" || report.Session != "live" {
				t.Errorf("status: err %v, output %s, want logged in, service_account and live", err, out)
			}
			if !slices.Equal(report.Scopes, held) {
				t.Errorf("status listed scopes %v, want %v", report.Scopes, held)
			}
			out, err = runService(t, "auth", "token")
			if want := tokenFor(held); err != nil || strings.TrimSpace(out) != want {
				t.Errorf("auth token: err %v, printed %q, want %q", err, out, want)
			}
		})
	}

	for _, refused := range []struct {
		name   string
		held   []string
		secret string
		want   string
	}{
		{"a refused secret", serviceScopes, "wrong-secret", "SLATE_CLIENT_SECRET"},
		{"a client holding no scope", nil, serviceSecret, "holds none of slate's scopes"},
	} {
		t.Run(refused.name, func(t *testing.T) {
			asService(t, scopedIDP(t, false, refused.held...).URL, "http://127.0.0.1:9", refused.secret)
			out, err := runService(t, "auth", "status")
			if exitCodeFor(err) != 1 || !strings.Contains(out, refused.want) || strings.Contains(out, "auth login") {
				t.Errorf("status: exit %d, output %q, want 1 and %q", exitCodeFor(err), out, refused.want)
			}
		})
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

// routePatterns compiles each scope's route templates. A {name} in them is a
// UUID, a record's number, or a worklog's <number>.<sequence>. Matched as any
// segment, /worklogs/{name} would also take /worklogs/current, which FastAPI
// routes to another handler. A {name:path} is FastAPI's path converter, which
// takes any segments, and an initiative is named by its name. A document is
// named by a word under its record.
func routePatterns(reaches map[string][]string) map[string][]*regexp.Regexp {
	reference := `(?:[0-9]+(?:\.[0-9]+)?|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`
	wildcard := regexp.MustCompile(`\{[^}]*\}`)
	patterns := map[string][]*regexp.Regexp{}
	for scope, templates := range reaches {
		for _, template := range templates {
			var pattern strings.Builder
			last := 0
			for _, at := range wildcard.FindAllStringIndex(template, -1) {
				pattern.WriteString(regexp.QuoteMeta(template[last:at[0]]))
				switch {
				case strings.HasSuffix(template[at[0]:at[1]], ":path}"):
					pattern.WriteString(".+")
				case strings.HasSuffix(template, "/documents/{record}/{name}") && template[at[0]:at[1]] == "{name}":
					pattern.WriteString(`[a-z][a-z0-9-]*`)
				default:
					pattern.WriteString(reference)
				}
				last = at[1]
			}
			pattern.WriteString(regexp.QuoteMeta(template[last:]))
			patterns[scope] = append(patterns[scope], regexp.MustCompile("^"+pattern.String()+"$"))
		}
	}
	return patterns
}

// Each command a service may run is run as one, through a provider holding
// every scope the API lists and an API gating each request on the scopes its
// token carries. So a command whose declared scopes miss a route it sends
// fails here, as does a declared scope none of its runs needs, a command
// reaching the API that declares nothing, and a declaring command with no run
// below. A scope the CLI spells differently from the API is refused by the
// provider.
func TestService_EachCommandSendsOnlyRoutesItsOwnScopesReach(t *testing.T) {
	reaches := apiScopeTable(t)
	if listed := slices.Sorted(maps.Keys(reaches)); !slices.Equal(listed, slices.Sorted(slices.Values(serviceScopes))) {
		t.Errorf("the CLI knows the scopes %v, and the API lists %v", serviceScopes, listed)
	}
	patterns := routePatterns(reaches)
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
	calls := []struct {
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
		{[]string{"dependencies", "tree", "--json"}, `[]`, nil},
		{[]string{"initiatives", "list", "--json"}, `[]`, nil},
		{
			[]string{"initiatives", "show", "Practical help guides", "--json"},
			`[]`,
			map[string]string{"/api/v1/initiatives/Practical help guides": `{}`},
		},
		{[]string{"ideas", "list", "--json"}, `[]`, nil},
		{[]string{"ideas", "show", "412", "--json"}, `{}`, nil},
		{[]string{"documents", "list", "412", "--json"}, `[]`, nil},
		{[]string{"documents", "show", "412", "plan", "--json"}, `{}`, nil},
		{[]string{"comments", "list", "412", "--json"}, `[]`, nil},
		{[]string{"vocabulary", "--json"}, `{}`, nil},
		{[]string{"totals", "412", "--json"}, `{}`, nil},
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
		{
			[]string{"worklogs", "link", "--session", "run-pod-session", "--commit", "4fca8f1"},
			wroteWorklog,
			map[string]string{"/api/v1/worklogs/current": worklog},
		},
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
		{[]string{"initiatives", "edit", "Practical help guides", "--priority", "high"}, `{}`, nil},
		{[]string{"initiatives", "complete", "Practical help guides"}, `{}`, nil},
		{[]string{"initiatives", "drop", "Practical help guides", "--reason", "Merged into the help initiative"}, `{}`, nil},
		{[]string{"initiatives", "shelve", "Practical help guides"}, `{}`, nil},
		{[]string{"initiatives", "reopen", "Practical help guides"}, `{}`, nil},
	}

	var (
		mu sync.Mutex
		// used is each command's scopes that reached a request one of its runs sent.
		used = map[string]map[string]bool{}
	)
	for _, call := range calls {
		command, _, err := NewRootCommand().Find(call.args)
		if err != nil {
			t.Fatalf("%v names no command: %v", call.args, err)
		}
		path := command.CommandPath()
		t.Run(strings.Join(call.args, " "), func(t *testing.T) {
			var sent int
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent++
				request := r.Method + " " + r.URL.Path
				var reached bool
				for _, scope := range tokenScopes(r.Header.Get("Authorization")) {
					if slices.ContainsFunc(patterns[scope], func(re *regexp.Regexp) bool { return re.MatchString(request) }) {
						reached = true
						mu.Lock()
						if used[path] == nil {
							used[path] = map[string]bool{}
						}
						used[path][scope] = true
						mu.Unlock()
					}
				}
				if !reached {
					t.Errorf("sent %s on a token for %v, whose scopes the API lists it under none of", request, tokenScopes(r.Header.Get("Authorization")))
				}
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
			if sent == 0 {
				t.Fatal("sent nothing to hold against the scopes")
			}
		})
	}

	// auth and config take --api to say which deployment they mean, and ask a
	// grant of their own or none.
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Name() == "auth" || cmd.Name() == "config" {
			return
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
		if cmd.HasSubCommands() || cmd.Flag("api") == nil {
			return
		}
		grant, declared := declaredGrant(cmd)
		switch {
		case !declared:
			t.Errorf("%s reaches the API and declares no service scopes", cmd.CommandPath())
		case grant.held, len(grant.scopes) == 0:
		case used[cmd.CommandPath()] == nil:
			t.Errorf("%s declares %v, and no run above holds them against its routes", cmd.CommandPath(), grant.scopes)
		default:
			for _, scope := range grant.scopes {
				if !used[cmd.CommandPath()][scope] {
					t.Errorf("%s declares %s, which no route its runs above sent needs", cmd.CommandPath(), scope)
				}
			}
		}
	}
	walk(NewRootCommand())
}

func TestHandedToken_SearchCarriesTheTokenTheCommandHands(t *testing.T) {
	var authorization string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(api.Close)
	handToken(t, api.URL, "handed-access-token")

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
