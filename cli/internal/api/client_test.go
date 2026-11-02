package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// staticTokenClient mimics the oauth2 client the CLI injects: an http.Client
// whose transport stamps a fixed bearer token on every request, so tests can
// assert the client forwards Authorization without a real token source.
func staticTokenClient(token string) *http.Client {
	return &http.Client{Transport: bearerTransport{token: token}}
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(req)
}

// recordQuery serves body and captures the raw query string, because an absent
// filter and an empty one have to reach the server as different requests.
func recordQuery(t *testing.T, body string) (*Client, *string) {
	t.Helper()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, staticTokenClient("t")), &got
}

// recordRequest serves response and captures the method, path and decoded JSON
// body of the last request.
func recordRequest(t *testing.T, response string) (*Client, *string, *string, *map[string]any) {
	t.Helper()
	var method, path string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		body = nil
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, staticTokenClient("t")), &method, &path, &body
}

func strptr(s string) *string { return &s }

func TestListInitiatives_AuthenticatesUnderTheAPIPrefix(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	client := New(srv.URL+"/", staticTokenClient("abc123"))
	if _, err := client.ListInitiatives(context.Background(), "", "", nil); err != nil {
		t.Fatalf("ListInitiatives: %v", err)
	}

	if gotAuth != "Bearer abc123" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer abc123")
	}
	if gotPath != "/api/v1/initiatives" {
		t.Errorf("request path = %q, want it under /api/v1", gotPath)
	}
}

func TestGetIssue_NotFoundCarriesTheAPIsMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Not Found","message":"issue 598 not found"}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, staticTokenClient("t")).GetIssue(context.Background(), "598", "")

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if !apiErr.NotFound() {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
	if apiErr.Message != "issue 598 not found" {
		t.Errorf("Message = %q", apiErr.Message)
	}
}

func TestGetIssue_UnauthorizedFlagged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := New(srv.URL, staticTokenClient("t")).GetIssue(context.Background(), "1", "")

	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.Unauthorized() {
		t.Errorf("err = %v, want an APIError reporting Unauthorized", err)
	}
}

// A non-JSON error body (a proxy page, a login redirect) must still yield a
// usable status-only APIError rather than a decode failure.
func TestSend_NonJSONErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
	}))
	defer srv.Close()

	_, err := New(srv.URL, staticTokenClient("t")).ListInitiatives(context.Background(), "", "", nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if apiErr.StatusCode != http.StatusBadGateway {
		t.Errorf("StatusCode = %d, want 502", apiErr.StatusCode)
	}
	if apiErr.Message != "" {
		t.Errorf("Message = %q, want empty for non-JSON body", apiErr.Message)
	}
}
