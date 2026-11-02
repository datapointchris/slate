package api

import (
	"net/url"
	"strconv"
	"time"
)

// applyLimit writes a row cap into params, or writes nothing when there is no
// cap to apply. One helper across every list read because --limit means the
// same thing on each: at most this many rows, server-side.
//
// A nil limit is the absence of a cap and is not sent, which is what asks for
// every row. A limit of zero is a row count the caller can mean, so it is sent
// as `limit=0` and the API answers with nothing. Folding the two together
// answers a caller computing its own bound with the whole collection.
func applyLimit(params url.Values, limit *int) {
	if limit == nil {
		return
	}
	params.Set("limit", strconv.Itoa(*limit))
}

// Window is the span of instants a list read narrows to. Since is the first
// instant inside it and Until the first instant past it. A zero end is open.
type Window struct {
	Since time.Time
	Until time.Time
}

// DayZone is the IANA zone the API reads a calendar day in. Empty leaves the
// server to read it in the user's preference.
type DayZone string

// applyWindow writes a window into params as RFC 3339 instants. Either end
// narrows on its own, and an open one is not sent.
func applyWindow(params url.Values, w Window) {
	if !w.Since.IsZero() {
		params.Set("since", w.Since.Format(time.RFC3339Nano))
	}
	if !w.Until.IsZero() {
		params.Set("until", w.Until.Format(time.RFC3339Nano))
	}
}

// repoQuery renders the repo filter. A nil pointer asks for everything; a
// pointer to "" asks for the untagged rows, which is a different question and
// so has to survive as an explicit empty parameter rather than being dropped.
func repoQuery(repo *string) url.Values {
	query := url.Values{}
	if repo != nil {
		query.Set("repo", *repo)
	}
	return query
}

func withQuery(path string, query url.Values) string {
	if len(query) == 0 {
		return path
	}
	return path + "?" + query.Encode()
}
