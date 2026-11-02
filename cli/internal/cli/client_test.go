package cli

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/datapointchris/slate/cli/internal/api"
)

func TestHandleAPIError_NotLoggedInPointsAtLogin(t *testing.T) {
	got := handleAPIError(fmt.Errorf("wrapped: %w", errNeedsLogin))
	if !strings.Contains(got.Error(), "slate auth login") {
		t.Errorf("got %q, want the login hint", got)
	}
}

// A revoked or expired grant fails at the token endpoint, so it reaches the
// command as the transport error of whichever request triggered the refresh.
// Without this mapping the terminal gets the request URL and the raw OAuth
// error description instead of the one thing worth doing about it.
func TestHandleAPIError_RefusedRefreshPointsAtLogin(t *testing.T) {
	retrieveErr := &oauth2.RetrieveError{
		Response:  &http.Response{StatusCode: http.StatusBadRequest},
		Body:      []byte(`{"error":"invalid_grant"}`),
		ErrorCode: "invalid_grant",
	}
	// The shape the API client actually produces: an *url.Error from the HTTP
	// round trip, wrapped again with the base URL.
	wrapped := fmt.Errorf("reach the slate API at %s: %w", "https://slate.example.com",
		&url.Error{Op: "Get", URL: "https://slate.example.com/api/v1/issues", Err: retrieveErr})

	got := handleAPIError(wrapped)
	if !strings.Contains(got.Error(), "slate auth login") {
		t.Errorf("got %q, want the login hint", got)
	}
	if strings.Contains(got.Error(), "invalid_grant") {
		t.Errorf("got %q, want the raw OAuth error kept out of the message", got)
	}
}

func TestHandleAPIError_UnauthorizedPointsAtLogin(t *testing.T) {
	got := handleAPIError(&api.APIError{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized"})
	if !strings.Contains(got.Error(), "slate auth login") {
		t.Errorf("got %q, want the login hint", got)
	}
}

func TestHandleAPIError_AConflictIsTheAPIsSentence(t *testing.T) {
	conflict := &api.APIError{StatusCode: http.StatusConflict, Status: "409 Conflict", Message: "#906 is claimed by session-one"}
	got := handleAPIError(conflict)
	if got.Error() != conflict.Message {
		t.Errorf("got %q, want the API's sentence alone", got)
	}
	var apiErr *api.APIError
	if !errors.As(got, &apiErr) || apiErr.StatusCode != http.StatusConflict {
		t.Errorf("got %#v, want the status still reachable", got)
	}
}

func TestHandleAPIError_PassesOtherErrorsThrough(t *testing.T) {
	original := errors.New("the network is on fire")
	if got := handleAPIError(original); !errors.Is(got, original) {
		t.Errorf("got %q, want the original error preserved", got)
	}
}
