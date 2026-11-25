// Package api is the slate CLI's typed client for the slate API. It depends on
// the wire contract (the JSON shapes the API returns), not the server's
// internal Python models, so the two can evolve independently as long as the
// JSON is stable. Every request carries the access token supplied by the
// injected *http.Client, which the API verifies against its OIDC provider's
// JWKS.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// prefix is where the API serves every resource route on its host; a path
// passed to get or send is below it.
const prefix = "/api/v1"

// Client talks to the slate API at baseURL. The http.Client is expected to
// inject the Authorization: Bearer header itself (via an oauth2 token-source
// transport), so the api package never handles tokens directly.
type Client struct {
	baseURL string
	http    *http.Client
	wrap    func(error) error
}

// Option sets how a Client behaves beyond its URL and transport.
type Option func(*Client)

// WrapErrors passes every error a request returns through wrap, so the caller
// can attach what it built the client from.
func WrapErrors(wrap func(error) error) Option {
	return func(c *Client) { c.wrap = wrap }
}

// New builds a client for baseURL. Trailing slashes are trimmed because a
// request's URL is baseURL, then prefix, then the path, and prefix starts with
// its own slash: a base ending in one would put a double slash before /api/v1.
func New(baseURL string, httpClient *http.Client, options ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    httpClient,
		wrap:    func(err error) error { return err },
	}
	for _, option := range options {
		option(c)
	}
	return c
}

// APIError is a non-2xx response, carrying the HTTP status and the API's decoded
// error message so callers can branch on StatusCode (401 → re-login, 404 → not
// found) and surface the server's message.
type APIError struct {
	StatusCode int
	Status     string
	Message    string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("API request failed (%s): %s", e.Status, e.Message)
	}
	return fmt.Sprintf("API request failed (%s)", e.Status)
}

// Unauthorized reports whether the error is a 401 — the CLI translates this into
// a "run `slate auth login`" hint rather than a raw API error.
func (e *APIError) Unauthorized() bool { return e.StatusCode == http.StatusUnauthorized }

// Forbidden reports whether the error is a 403.
func (e *APIError) Forbidden() bool { return e.StatusCode == http.StatusForbidden }

// NotFound reports whether the error is a 404.
func (e *APIError) NotFound() bool { return e.StatusCode == http.StatusNotFound }

// get issues a GET to path (e.g. "/projects") and decodes a 2xx JSON body into
// out. A non-2xx response becomes an *APIError.
func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.send(ctx, http.MethodGet, path, nil, out)
}

// getWithHeader is get, and also returns the response's header, for a read
// that answers a count beside its body.
func (c *Client) getWithHeader(ctx context.Context, path string, out any) (http.Header, error) {
	return c.request(ctx, http.MethodGet, path, nil, out)
}

// send issues an HTTP request to path with an optional JSON body, and decodes a
// 2xx JSON body into out. A nil body sends no payload; a nil out discards the
// response body (for 204 No Content and similar). A non-2xx response becomes an
// *APIError regardless of out.
func (c *Client) send(ctx context.Context, method, path string, body, out any) error {
	_, err := c.request(ctx, method, path, body, out)
	return err
}

// request is send, returning the 2xx response's header.
//
// A request that got no response, such as one whose connection was reset, is
// sent once more after a second where its method is idempotent. A POST is not,
// because the server may have acted on it before the connection dropped.
func (c *Client) request(ctx context.Context, method, path string, body, out any) (http.Header, error) {
	header, err := c.exchange(ctx, method, path, body, out)
	var unreached *unreachedError
	if errors.As(err, &unreached) && idempotent[method] {
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			header, err = c.exchange(ctx, method, path, body, out)
		}
	}
	if err != nil {
		return nil, c.wrap(err)
	}
	return header, nil
}

var idempotent = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPut: true, http.MethodDelete: true,
}

// unreachedError is a request that got no HTTP response. within is the http.Client's Timeout
// where that bound ran out, rather than the caller's context.
type unreachedError struct {
	baseURL string
	within  time.Duration
	err     error
}

func (e *unreachedError) Error() string {
	if e.within > 0 {
		return fmt.Sprintf("the slate API at %s gave no answer within %s", e.baseURL, e.within)
	}
	return fmt.Sprintf("reach the slate API at %s: %v", e.baseURL, e.err)
}

func (e *unreachedError) Unwrap() error { return e.err }

func (c *Client) exchange(ctx context.Context, method, path string, body, out any) (http.Header, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+prefix+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		unreached := &unreachedError{baseURL: c.baseURL, err: err}
		var transport *url.Error
		if errors.As(err, &transport) && transport.Timeout() && ctx.Err() == nil {
			unreached.within = c.http.Timeout
		}
		return nil, unreached
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeAPIError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.Header, nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return nil, fmt.Errorf("decode API response: %w", err)
	}
	return resp.Header, nil
}

// decodeAPIError builds an *APIError from a non-2xx response, best-effort
// decoding the error envelope. The API writes errors as
// {"error": "<status phrase>", "message": "<reason>"}, and the reason is always
// a string; a body that is not that shape (a proxy error page, a login
// redirect) still yields a usable status-only error.
func decodeAPIError(resp *http.Response) error {
	apiErr := &APIError{StatusCode: resp.StatusCode, Status: resp.Status}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))

	var envelope struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		switch {
		case envelope.Message != "":
			apiErr.Message = envelope.Message
		case envelope.Error != "":
			apiErr.Message = envelope.Error
		}
	}
	return apiErr
}

// mergeClearNulls marshals a typed partial-update body to a JSON object and adds
// an explicit null for each field named in clear (API JSON keys). Sending an
// explicit null is the only way to clear a field, because the typed struct's
// omitempty drops it from the payload entirely and the API reads an absent
// field as "leave it alone".
func mergeClearNulls(input any, clear []string) (map[string]json.RawMessage, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	body := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	for _, field := range clear {
		body[field] = json.RawMessage("null")
	}
	return body, nil
}
