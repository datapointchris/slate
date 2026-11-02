package cli

import (
	"errors"

	"github.com/datapointchris/slate/cli/internal/api"
)

// notFound reports whether err is the API saying a resource is not there, and
// the line naming which one.
//
// The subject is the API's, not one built from the arguments, because only the
// API knows which id was missing: `slate issues remove-comment 598 4` can 404
// on either number and the message names the one that did.
//
// The "API request failed (404 Not Found)" prefix goes with it. That reports how
// the answer arrived, and a 404 is an answer rather than a failure to get one.
//
// An empty Message is the case where it is not: a proxy error page or a
// login redirect decodes to a status and nothing else, and the resource was
// never reached. Reporting no subject leaves the status line alone rather than
// replacing it with a resource claim nothing established.
func notFound(err error) (string, bool) {
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || !apiErr.NotFound() {
		return "", false
	}
	return apiErr.Message, true
}
