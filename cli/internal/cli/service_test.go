package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

// readAPIDocument decodes a file tests/api/test_client_scopes.py writes.
func readAPIDocument(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile("../../../tests/api/testdata/" + name)
	if err != nil {
		t.Fatalf("read %s — run tests/api/test_client_scopes.py to write it: %v", name, err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
}

// apiScopeTable is the API's routes by scope.
func apiScopeTable(t *testing.T) map[string][]string {
	t.Helper()
	var reaches map[string][]string
	readAPIDocument(t, "client-scopes.json", &reaches)
	return reaches
}

// serviceIDP is scopedIDP for a client registered for every scope the API lists.
func serviceIDP(t *testing.T) *httptest.Server {
	t.Helper()
	return scopedIDP(t, provider{held: slices.Collect(maps.Keys(apiScopeTable(t)))})
}

// provider is how a fake identity provider answers serviceClientID.
type provider struct {
	held []string
	// narrows grants the held part of a request naming a scope the client
	// lacks, as RFC 6749 §3.3 allows, where Authelia refuses it with
	// invalid_scope.
	narrows bool
	// failing is a scope whose every request the provider answers with a 500.
	failing string
}

// scopedIDP grants serviceClientID, presenting serviceSecret in a Basic header,
// a token for the scopes it asks that p holds. The response names what it
// granted, and the token is that list.
func scopedIDP(t *testing.T, p provider) *httptest.Server {
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
		asked := strings.Fields(r.FormValue("scope"))
		if p.failing != "" && slices.Contains(asked, p.failing) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		granted := []string{}
		for _, scope := range asked {
			switch {
			case slices.Contains(p.held, scope):
				granted = append(granted, scope)
			case !p.narrows:
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
			asService(t, scopedIDP(t, provider{held: refusal.held, narrows: refusal.narrows}).URL, api.URL, refusal.secret)

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

// statusJSON is what `auth status --json` reports of a service.
type statusJSON struct {
	LoggedIn bool   `json:"logged_in"`
	Type     string `json:"type"`
	Session  string `json:"session"`
	Scopes   []struct {
		Scope string `json:"scope"`
		Grant string `json:"grant"`
	} `json:"scopes"`
}

func (s statusJSON) answered(grant string) []string {
	var scopes []string
	for _, answer := range s.Scopes {
		if answer.Grant == grant {
			scopes = append(scopes, answer.Scope)
		}
	}
	return scopes
}

// A service client holds only the scopes its commands need, so status asks for
// each alone and reports each answer, and auth token asks for all it holds.
func TestService_StatusListsTheScopesTheClientHolds(t *testing.T) {
	held := []string{scopeRecordsRead, scopeClaimsHold}
	for _, live := range []struct {
		name       string
		provider   provider
		granted    []string
		unanswered []string
	}{
		{"a provider refusing an unheld scope", provider{held: held}, held, nil},
		{"a provider narrowing to the held scopes", provider{held: held, narrows: true}, held, nil},
		{"a provider failing its answer for one held scope", provider{held: held, failing: scopeClaimsHold}, held[:1], held[1:]},
	} {
		t.Run(live.name, func(t *testing.T) {
			asService(t, scopedIDP(t, live.provider).URL, "http://127.0.0.1:9", serviceSecret)
			out, err := runService(t, "auth", "status", "--json")
			var report statusJSON
			if err != nil || json.Unmarshal([]byte(out), &report) != nil || !report.LoggedIn || report.Type != "service_account" || report.Session != "live" {
				t.Errorf("status: err %v, output %s, want logged in, service_account and live", err, out)
			}
			if got := report.answered("granted"); !slices.Equal(got, live.granted) {
				t.Errorf("status found %v granted, want %v", got, live.granted)
			}
			if got := report.answered("unknown"); !slices.Equal(got, live.unanswered) {
				t.Errorf("status found %v unanswered, want %v", got, live.unanswered)
			}
			out, err = runService(t, "auth", "token")
			if want := tokenFor(live.granted); err != nil || strings.TrimSpace(out) != want {
				t.Errorf("auth token: err %v, printed %q, want %q", err, out, want)
			}
		})
	}

	// The two refusals have opposite remedies, so --json tells them apart.
	for _, refused := range []struct {
		name     string
		provider provider
		secret   string
		grant    string
		want     string
	}{
		{"a refused secret", provider{held: serviceScopes}, "wrong-secret", "unknown", config.ClientSecretEnv},
		{"a client holding no scope", provider{}, serviceSecret, "refused", "none of slate's scopes"},
	} {
		t.Run(refused.name, func(t *testing.T) {
			asService(t, scopedIDP(t, refused.provider).URL, "http://127.0.0.1:9", refused.secret)
			out, err := runService(t, "auth", "status")
			if exitCodeFor(err) != 1 || !strings.Contains(out, refused.want) || strings.Contains(out, "auth login") {
				t.Errorf("status: exit %d, output %q, want 1 and %q", exitCodeFor(err), out, refused.want)
			}
			out, _ = runService(t, "auth", "status", "--json")
			var report statusJSON
			if err := json.Unmarshal([]byte(out), &report); err != nil || len(report.answered(refused.grant)) != len(serviceScopes) {
				t.Errorf("status --json: %s, want every scope answered %s", out, refused.grant)
			}
			_, err = runService(t, "auth", "token")
			if err == nil || !strings.Contains(err.Error(), serviceClientID) || !strings.Contains(err.Error(), refused.want) {
				t.Errorf("auth token: %v, want it to name %s and %q", err, serviceClientID, refused.want)
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

// A rejection is worded from the deployment the client was built from, so
// under --api it names that API rather than the config file's.
func TestService_ARejectedTokenNamesTheAPIItWasSentTo(t *testing.T) {
	idp := serviceIDP(t)
	var api *httptest.Server
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": api.URL, "authorization_servers": []string{idp.URL}})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(api.Close)
	asService(t, idp.URL, "http://127.0.0.1:9", serviceSecret)

	_, err := runService(t, "search", "sync", "--api", api.URL)
	if err == nil || !strings.Contains(err.Error(), api.URL+" rejected the token issued to service client "+serviceClientID) {
		t.Errorf("got %v, want the --api URL and the client named", err)
	}
}

// scopedBody is what a scope may send to a route whose handler narrows the
// body: only these fields, and a status among Statuses where it names any.
type scopedBody struct {
	Fields   []string `json:"fields"`
	Statuses []string `json:"statuses"`
}

func (b scopedBody) admits(body map[string]any) bool {
	for field := range body {
		if !slices.Contains(b.Fields, field) {
			return false
		}
	}
	status, _ := body["status"].(string)
	return len(b.Statuses) == 0 || slices.Contains(b.Statuses, status)
}

// scopeGate admits a request as the API does a client holding one scope: its
// route is among the scope's, and where the handler narrows that route's
// body, the body is one the scope may send.
type scopeGate struct {
	routes map[string][]route
	// bodies is keyed by route template, then by scope.
	bodies map[string]map[string]scopedBody
}

type route struct {
	template string
	pattern  *regexp.Regexp
}

func apiScopeGate(t *testing.T) scopeGate {
	t.Helper()
	gate := scopeGate{routes: routePatterns(apiScopeTable(t))}
	readAPIDocument(t, "scoped-bodies.json", &gate.bodies)
	return gate
}

// sentRequest is r's method and path, its body, and the fields the body sets.
func sentRequest(r *http.Request) (request string, raw []byte, fields map[string]any) {
	raw, _ = io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &fields)
	return r.Method + " " + r.URL.Path, raw, fields
}

func (g scopeGate) admits(scope, request string, body map[string]any) bool {
	for _, r := range g.routes[scope] {
		if r.pattern.MatchString(request) {
			narrowed, ok := g.bodies[r.template][scope]
			return !ok || narrowed.admits(body)
		}
	}
	return false
}

// routePatterns compiles each scope's route templates. A {name} in them is a
// UUID, a record's number, or a worklog's <number>.<sequence>. Matched as any
// segment, /worklogs/{name} would also take /worklogs/current, which FastAPI
// routes to another handler. A {name:path} is FastAPI's path converter, which
// takes any segments, and an initiative is named by its name. A document is
// named by a word under its record.
func routePatterns(reaches map[string][]string) map[string][]route {
	reference := `(?:[0-9]+(?:\.[0-9]+)?|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`
	wildcard := regexp.MustCompile(`\{[^}]*\}`)
	patterns := map[string][]route{}
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
			patterns[scope] = append(patterns[scope], route{template, regexp.MustCompile("^" + pattern.String() + "$")})
		}
	}
	return patterns
}

// Each command a service may run is run as one, through a provider holding
// every scope the API lists and an API admitting each request as the real one
// admits the scopes its token carries. Each person-only command runs on a
// token carrying every scope, and one of its requests must be admitted by
// none. So this fails on a route or body the declared scopes miss, a declared
// scope none of a command's runs needs, a person-only command some scope now
// reaches, a command reaching the API that declares nothing, a declaring
// command with no run below, and a scope the CLI lists that the API does not.
func TestService_EachCommandSendsOnlyRoutesItsOwnScopesReach(t *testing.T) {
	if listed := slices.Sorted(maps.Keys(apiScopeTable(t))); !slices.Equal(listed, slices.Sorted(slices.Values(serviceScopes))) {
		t.Errorf("the CLI knows the scopes %v, and the API lists %v", serviceScopes, listed)
	}
	gate := apiScopeGate(t)
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
		{
			[]string{"show", "412", "--json", "--changes", "5"},
			`{"kind":"idea","number":412,"record":{}}`,
			map[string]string{"/api/v1/records/412/changes": `[]`},
		},
		{[]string{"issues", "show", "412", "--json"}, `{}`, nil},
		{[]string{"issues", "show", "019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5b", "--json"}, `{}`, nil},
		{[]string{"dependencies", "tree", "--json"}, `[]`, nil},
		{[]string{"initiatives", "list", "--json"}, `[]`, nil},
		{
			[]string{"initiatives", "show", "Practical help guides", "--json"},
			`[]`,
			map[string]string{"/api/v1/initiatives/Practical help guides": `{}`},
		},
		{[]string{"initiatives", "progress", "Practical help guides", "--json"}, `[]`, nil},
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
				request, raw, fields := sentRequest(r)
				var reached bool
				for _, scope := range tokenScopes(r.Header.Get("Authorization")) {
					if gate.admits(scope, request, fields) {
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
					t.Errorf("sent %s %s on a token for %v, which the API admits under none of them", request, raw, tokenScopes(r.Header.Get("Authorization")))
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
					err := json.Unmarshal(raw, &write)
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

	triageIssue := strings.Replace(openIssue, `"open"`, `"triage"`, 1)
	completedIssue := strings.Replace(openIssue, `"open"`, `"completed"`, 1)
	idea := `{"id":"019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5d","number":1860,"title":"t","status":"open"}`
	plan := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(plan, []byte("# Plan\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	personOnlyCalls := []struct {
		args []string
		body string
	}{
		{[]string{"labels", "list", "--json"}, `[]`},
		{[]string{"labels", "show", "area-api"}, `{}`},
		{[]string{"labels", "create", "--slug", "area-db", "--group", "area"}, `{}`},
		{[]string{"labels", "edit", "area-db", "--group", "area"}, `{}`},
		{[]string{"labels", "delete", "area-db", "--yes"}, `{}`},
		{[]string{"ideas", "create", "--title", "Run review rounds on a schedule"}, idea},
		{[]string{"ideas", "edit", "1860", "--priority", "urgent"}, idea},
		{[]string{"ideas", "link", "1860", "inbox:4102"}, idea},
		{[]string{"ideas", "promote", "1860"}, idea},
		{[]string{"dependencies", "add", "412", "--depends-on", "411"}, openIssue},
		{[]string{"dependencies", "remove", "412", "--depends-on", "411"}, openIssue},
		{[]string{"documents", "write", "412", "plan", "--file", plan}, `{}`},
		{[]string{"comments", "remove", "57", "--yes"}, `{}`},
		{[]string{"issues", "delete", "412", "--yes"}, openIssue},
		{[]string{"issues", "accept", "412"}, triageIssue},
		{[]string{"issues", "cancel", "412", "--reason", "the router rewrite removed the path"}, openIssue},
		{[]string{"issues", "reopen", "412"}, completedIssue},
		{[]string{"initiatives", "delete", "Practical help guides", "--yes"}, `{"id":"019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5c","name":"Practical help guides"}`},
	}
	ranAsPerson := map[string]bool{}
	for _, call := range personOnlyCalls {
		command, _, err := NewRootCommand().Find(call.args)
		if err != nil {
			t.Fatalf("%v names no command: %v", call.args, err)
		}
		path := command.CommandPath()
		ranAsPerson[path] = true
		t.Run(strings.Join(call.args, " "), func(t *testing.T) {
			var sent, refused []string
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				request, raw, fields := sentRequest(r)
				sent = append(sent, request+" "+string(raw))
				if !slices.ContainsFunc(serviceScopes, func(scope string) bool { return gate.admits(scope, request, fields) }) {
					refused = append(refused, request)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"message":"no scope reaches this"}`))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(call.body))
			}))
			t.Cleanup(api.Close)
			handToken(t, api.URL, tokenFor(serviceScopes))
			_, err := runService(t, call.args...)
			if len(refused) == 0 {
				t.Errorf("%s declares personOnly(), and a client holding every scope is admitted all it sent: %v (%v)", path, sent, err)
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
		case grant.held:
		case len(grant.scopes) == 0:
			if !ranAsPerson[cmd.CommandPath()] {
				t.Errorf("%s declares personOnly(), and no run above holds it against the routes", cmd.CommandPath())
			}
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
