package cli

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

type scriptedTransport struct {
	answers []any
	calls   int
	bodies  []string
}

func (s *scriptedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		s.bodies = append(s.bodies, string(body))
	}
	answer := s.answers[min(s.calls, len(s.answers)-1)]
	s.calls++
	if err, ok := answer.(error); ok {
		return nil, err
	}
	return &http.Response{StatusCode: answer.(int), Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
}

func TestARetrySendsOnlyWhatCannotHappenTwice(t *testing.T) {
	lookup := &net.DNSError{Err: "no such host", Name: "slate.example.com"}
	reset := &net.OpError{Op: "read", Err: &timeoutError{}}
	for _, c := range []struct {
		name    string
		method  string
		answers []any
		calls   int
		status  int
	}{
		{"a read retries a 503 until it answers", http.MethodGet, []any{503, 200}, 2, 200},
		{"a read gives up after its attempts", http.MethodGet, []any{503}, requestAttempts, 503},
		{"a read takes a 404 as the answer", http.MethodGet, []any{404}, 1, 404},
		{"a read retries a lost lookup", http.MethodGet, []any{lookup, 200}, 2, 200},
		{"a read retries a lost connection", http.MethodGet, []any{reset, 200}, 2, 200},
		{"a write retries a lost lookup and keeps its body", http.MethodPatch, []any{lookup, 200}, 2, 200},
		{"a write takes a 503 as the answer", http.MethodPatch, []any{503}, 1, 503},
		{"a write never repeats a lost connection", http.MethodPatch, []any{reset}, 1, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			next := &scriptedTransport{answers: c.answers}
			transport := retryingTransport{next: next, pause: func(int, *http.Response) time.Duration { return 0 }}
			req, err := http.NewRequest(c.method, "https://slate.example.com/api/v1/issues/1", strings.NewReader("sent"))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := transport.RoundTrip(req)
			if next.calls != c.calls {
				t.Errorf("calls = %d, want %d", next.calls, c.calls)
			}
			if c.status == 0 {
				if err == nil {
					t.Error("want the error back")
				}
				return
			}
			if err != nil || resp.StatusCode != c.status {
				t.Fatalf("got %v, %v, want status %d", resp, err, c.status)
			}
			for _, body := range next.bodies {
				if body != "sent" {
					t.Errorf("a send carried %q", body)
				}
			}
		})
	}
}

type timeoutError struct{}

func (*timeoutError) Error() string   { return "i/o timeout" }
func (*timeoutError) Timeout() bool   { return true }
func (*timeoutError) Temporary() bool { return true }
