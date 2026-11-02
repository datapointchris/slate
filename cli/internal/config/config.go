// Package config resolves the slate CLI's settings, of which there are two kinds.
//
// Which deployment to talk to is one file, $XDG_CONFIG_HOME/slate/config.toml,
// read through goclilogin's config-file contract: its api, issuer and
// client_id keys, with --api the only thing that outranks them. None of the
// three is compiled in. Each names one installation, so a value shipped in the
// binary would point somebody else's copy at this one.
//
// MachineConfig is what this machine says about its own layout, read from
// config.yml beside it. It is the middle rung of the order every shared path
// resolves through: the SLATE_-prefixed variable, then the config key, then a
// default under slate's own XDG directory.
package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/datapointchris/goclilogin"
)

// product names the config file's directory.
const product = "slate"

// keyringService namespaces slate's entries in the OS keychain. It is a deployed
// identifier rather than a path-derived one, so it keeps its spelling
// independently of the module or binary name.
const keyringService = "slate-cli"

// The scopes a service client requests. The provider grants only the scopes a
// request names, so a request naming none gets a token the API refuses. It
// refuses the whole request when one names a scope the client does not hold.
// src/slate/api/client_scopes.py lists the routes each one reaches.
const (
	recordsReadScope   = "slate.records.read"
	claimsHoldScope    = "slate.claims.hold"
	issuesCloseScope   = "slate.issues.close"
	issuesPlanScope    = "slate.issues.plan"
	worklogsWriteScope = "slate.worklogs.write"
)

// ClientSecretEnv selects the client-credentials grant, for the client the
// config file names. It is read from the environment alone, so it is never
// written to the file and no command prints it.
const ClientSecretEnv = "SLATE_CLIENT_SECRET"

// Load reads the deployment every command talks to. api is the --api flag's
// value, and outranks the file's when it is not empty.
//
// A service missing a value is told which key to write rather than to log in,
// since a service has no login.
func Load(ctx context.Context, api string) (goclilogin.Deployment, error) {
	d, err := goclilogin.LoadDeployment(ctx, product, api)
	if d.HandsTokens() && IsService() {
		return d, fmt.Errorf("%s sets token_command and %s is set, so slate has two credentials; keep one",
			d.Path, ClientSecretEnv)
	}
	if !IsService() || !errors.Is(err, goclilogin.ErrNotConfigured) {
		return d, err
	}
	var missing []string
	for _, key := range []struct {
		name string
		set  goclilogin.Setting
	}{{"api", d.API}, {"issuer", d.Issuer}, {"client_id", d.ClientID}} {
		if key.set.Value == "" {
			missing = append(missing, key.name)
		}
	}
	return d, fmt.Errorf("%w: %s sets no %s; a service names its API, provider and client there, and only %s in its environment",
		goclilogin.ErrNotConfigured, d.Path, strings.Join(missing, " or "), ClientSecretEnv)
}

// ForLogin is the deployment `slate auth login` logs in to: the file's values,
// with whatever it lacks filled in from api, the API's metadata and clientID.
func ForLogin(ctx context.Context, api, clientID string) (goclilogin.Deployment, error) {
	return goclilogin.DeploymentForLogin(ctx, product, api, clientID)
}

// Path is where the deployment file goes, whether or not it exists.
func Path() string { return goclilogin.ConfigPath(product) }

// DefaultClientID is the client id login derives when neither the file nor
// --client-id names one: slate-cli-<short hostname>.
func DefaultClientID() string { return goclilogin.ClientID(product) }

// Login is the goclilogin view of a deployment: which provider to authenticate
// against, as which client, and where the token and its refresh lock live.
//
// StateDir is passed rather than left to goclilogin's default, which would put
// it under the keyring service name. The token file and refresh lock then sit
// beside slate's other state, at a path that does not move if the keyring
// service is ever renamed.
func Login(d goclilogin.Deployment) goclilogin.Config {
	return goclilogin.Config{
		Issuer:         d.Issuer.Value,
		ClientID:       d.ClientID.Value,
		KeyringService: keyringService,
		StateDir:       StatePath(),
	}
}

// ClientSecret is SLATE_CLIENT_SECRET, set only where a service runs slate with
// nobody present to approve a device login.
func ClientSecret() string { return os.Getenv(ClientSecretEnv) }

// IsService reports whether slate authenticates as a confidential service client
// rather than as the person who logged in on this machine.
func IsService() bool { return ClientSecret() != "" }

// Service is the goclilogin view of a deployment for the client-credentials
// grant. Nothing it obtains is stored, so it names no keyring or state
// directory.
func Service(d goclilogin.Deployment) goclilogin.ServiceClient {
	return goclilogin.ServiceClient{Issuer: d.Issuer.Value, ClientID: d.ClientID.Value, Scopes: []string{recordsReadScope, claimsHoldScope, issuesCloseScope, issuesPlanScope, worklogsWriteScope}}
}
