package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/datapointchris/goclilogin"
)

// Classifying a session lives in goclilogin, which tests it against a provider
// that rotates and revokes. What is slate's own is how each of the three states
// reads on the terminal.
func statusOutput(r statusReport) string {
	var out bytes.Buffer
	r.Configured = true
	printStatus(&out, r, goclilogin.Deployment{}, nil)
	return out.String()
}

func TestPrintStatus_NotLoggedInNamesTheLoginCommand(t *testing.T) {
	got := statusOutput(statusReport{ClientID: "slate-cli-archlinux"})
	if !strings.Contains(got, "slate auth login") {
		t.Errorf("got %q, want the login command named", got)
	}
}

func TestPrintStatus_RejectedNamesTheLoginCommand(t *testing.T) {
	got := statusOutput(statusReport{
		LoggedIn: true,
		ClientID: "slate-cli-archlinux",
		Issuer:   "https://auth.example.com",
		Session:  goclilogin.SessionRejected,
	})
	if !strings.Contains(got, "slate auth login") {
		t.Errorf("got %q, want the login command named", got)
	}
	if !strings.Contains(got, "rejected") {
		t.Errorf("got %q, want the rejection stated", got)
	}
}

func TestPrintStatus_UnverifiedSaysWhyItCouldNotAnswer(t *testing.T) {
	got := statusOutput(statusReport{
		LoggedIn:  true,
		ClientID:  "slate-cli-archlinux",
		Issuer:    "https://auth.example.com",
		ExpiresAt: "2026-08-22T06:13:47-04:00",
		Expired:   true,
		Session:   goclilogin.SessionUnverified,
	})
	if !strings.Contains(got, "unverified") {
		t.Errorf("got %q, want the unverified state stated", got)
	}
}
