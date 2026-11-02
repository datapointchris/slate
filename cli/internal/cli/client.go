package cli

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/goclilogin"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/datapointchris/slate/cli/internal/api"
	"github.com/datapointchris/slate/cli/internal/config"
)

// errNeedsLogin is returned by newAPIClient when there is no stored token. The
// resource commands print the login hint and exit 1; it is a normal state, not a
// crash, so it is kept distinct from a runtime error.
var errNeedsLogin = errors.New("not logged in")

// newAPIClient builds an authenticated API client for the running command. The
// oauth2 client injects (and refreshes) the bearer token on every request via
// goclilogin's token source, so resource commands never touch tokens directly.
//
// Every error after the deployment loads carries it, so handleAPIError words
// a credential failure from what the client was built from.
func newAPIClient(cmd *cobra.Command) (*api.Client, error) {
	deployment, err := config.Load(cmd.Context(), apiOverride)
	if err != nil {
		return nil, err
	}
	service := config.IsService()
	carry := func(err error) error { return &builtFrom{deployment: deployment, service: service, err: err} }
	source, err := tokenSource(cmd, deployment)
	if errors.Is(err, errNeedsLogin) {
		return nil, carry(err)
	}
	if err != nil {
		return nil, carry(fmt.Errorf("prepare API client: %w", err))
	}
	return api.New(deployment.API.Value, oauth2.NewClient(cmd.Context(), source), api.WrapErrors(carry)), nil
}

// builtFrom is an error from building or using an API client, carrying the
// deployment it was built from and whether it authenticated as a service.
type builtFrom struct {
	deployment goclilogin.Deployment
	service    bool
	err        error
}

func (e *builtFrom) Error() string { return e.err.Error() }

func (e *builtFrom) Unwrap() error { return e.err }

// tokenSource is the config file's token_command where it names one, the
// client-credentials grant for the scopes cmd declares when SLATE_CLIENT_SECRET
// is set, and otherwise the token this machine logged in for.
//
// A command that declares nothing is refused whoever runs it, so any test
// running it names the omission rather than a service finding it.
func tokenSource(cmd *cobra.Command, d goclilogin.Deployment) (oauth2.TokenSource, error) {
	ctx := cmd.Context()
	grant, declared := declaredGrant(cmd)
	if !declared {
		return nil, fmt.Errorf("%#q declares no service scopes; give its literal Annotations: scopesForService(...) or personOnly()", cmd.CommandPath())
	}
	if d.HandsTokens() {
		return goclilogin.CommandTokenSource(ctx, d.TokenCommand.Argv())
	}
	if config.IsService() {
		return serviceTokenSource(cmd, d, grant)
	}
	login := config.Login(d)
	source, err := goclilogin.TokenSource(ctx, login, goclilogin.NewTokenStore(login))
	if errors.Is(err, goclilogin.ErrNotLoggedIn) {
		return nil, errNeedsLogin
	}
	return source, err
}

// handleAPIError maps an error from a resource command to a message and exit
// code: not-logged-in, a token endpoint that refused the refresh, and 401 all
// point at `slate auth login` (exit 1); everything else is a runtime error (exit 1
// via Execute). A service client is never pointed at a login, because none
// exists for it. Returns the error to return from RunE.
func handleAPIError(err error) error {
	if errors.Is(err, errNeedsLogin) {
		return fmt.Errorf("not logged in — run `slate auth login`")
	}
	var (
		apiErr *api.APIError
		built  *builtFrom
	)
	errors.As(err, &built)
	// A handed token's remedy is whatever handed it, so it names the command.
	if built != nil && built.deployment.HandsTokens() {
		d := built.deployment
		var failed *goclilogin.TokenCommandError
		switch {
		case errors.As(err, &failed):
			return fmt.Errorf("token_command in %s handed no token: %w", d.Path, failed)
		case errors.As(err, &apiErr) && apiErr.Unauthorized():
			return fmt.Errorf("%s rejected the token %q handed", d.API.Value, d.TokenCommand)
		case errors.As(err, &apiErr) && apiErr.Forbidden() && apiErr.Message != "":
			return fmt.Errorf("the token %q handed: %s", d.TokenCommand, apiErr.Message)
		}
	}
	// A service has no login to renew, so it is pointed at the scopes it lacks,
	// or else at its id and secret.
	if built != nil && built.service {
		d := built.deployment
		var refused *scopesRefused
		switch {
		case errors.As(err, &refused):
			return refused
		case goclilogin.IsSessionRejected(err):
			return fmt.Errorf("%s refused service client %s — check client_id in %s, and %s", d.Issuer.Value, d.ClientID.Value, d.Path, config.ClientSecretEnv)
		case errors.As(err, &apiErr) && apiErr.Unauthorized():
			return fmt.Errorf("%s rejected the token issued to service client %s", d.API.Value, d.ClientID.Value)
		case errors.As(err, &apiErr) && apiErr.Forbidden() && apiErr.Message != "":
			return fmt.Errorf("service client %s: %s", d.ClientID.Value, apiErr.Message)
		}
	}
	// A refusal at the token endpoint arrives as the transport error of the
	// request that triggered the refresh, so the URL and the raw OAuth error
	// description are what reach the terminal unless they are named here.
	if goclilogin.IsSessionRejected(err) {
		return fmt.Errorf("session expired — run `slate auth login`")
	}
	if errors.As(err, &apiErr) && apiErr.Unauthorized() {
		return fmt.Errorf("session rejected by the API — run `slate auth login` to re-authenticate")
	}
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict && apiErr.Message != "" {
		return refusal{apiErr}
	}
	return err
}

// refusal is a 409 the API explained. Its sentence is the whole error, printed
// without the "API request failed (409 Conflict):" prefix APIError adds.
type refusal struct{ *api.APIError }

func (r refusal) Error() string { return r.Message }

func (r refusal) Unwrap() error { return r.APIError }

// handleArgumentAPIError is handleAPIError for a resource whose arguments the
// API validates, so its 422 is a usage error.
//
// The API refuses an unknown vocabulary value with a 422 naming the values that
// would have worked. Left as a generic failure it exits 1, and a caller that
// retries on 1 and fixes its arguments on 2 retries a typo forever.
func handleArgumentAPIError(err error) error {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnprocessableEntity && apiErr.Message != "" {
		return goclikit.UsageError(errors.New(apiErr.Message))
	}
	return handleAPIError(err)
}
