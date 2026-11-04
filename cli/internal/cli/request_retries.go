package cli

import (
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"time"
)

const (
	requestAttempts = 3
	longestPause    = 20 * time.Second
)

// retryingTransport tries a request again where the API gave no answer. A read
// retries any failure to reach it and a 429, 502, 503 or 504. A write retries only
// a failure before it was sent, because a retry after that could do it twice.
type retryingTransport struct {
	next  http.RoundTripper
	pause func(attempt int, resp *http.Response) time.Duration
}

func (t retryingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for attempt := 1; ; attempt++ {
		resp, err := t.next.RoundTrip(req)
		if attempt == requestAttempts || !worthRetrying(req.Method, resp, err) {
			return resp, err
		}
		pause := t.pause(attempt, resp)
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		select {
		case <-time.After(pause):
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
		req = req.Clone(req.Context())
		if req.GetBody != nil {
			body, bodyErr := req.GetBody()
			if bodyErr != nil {
				return nil, bodyErr
			}
			req.Body = body
		}
	}
}

func worthRetrying(method string, resp *http.Response, err error) bool {
	read := method == http.MethodGet
	if err != nil {
		return neverSent(err) || (read && unreachable(err))
	}
	if !read {
		return false
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// neverSent is a name that did not resolve or a connection that did not open.
func neverSent(err error) bool {
	var lookup *net.DNSError
	var op *net.OpError
	return errors.As(err, &lookup) || (errors.As(err, &op) && op.Op == "dial")
}

// unreachable is a reset, a timeout or a connection cut before the whole answer.
func unreachable(err error) bool {
	var network net.Error
	return errors.As(err, &network) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}

// randomPause waits what the server's Retry-After says, or else a random fraction
// of 2^(attempt-1) seconds, as the AWS CLI's standard mode does.
func randomPause(attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds >= 0 {
			return min(time.Duration(seconds)*time.Second, longestPause)
		}
	}
	window := time.Second << (attempt - 1)
	return min(time.Duration(rand.Int64N(int64(window))), longestPause)
}
