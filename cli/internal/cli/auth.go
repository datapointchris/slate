package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/datapointchris/goclikit"
	"github.com/datapointchris/goclilogin"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/datapointchris/slate/cli/internal/config"
)

// loginTimeout is a backstop only — the device code carries its own expiry and
// the poll stops there first.
const loginTimeout = 15 * time.Minute

func newAuthCommand() *cobra.Command {
	cmd := goclikit.AsNamespace(&cobra.Command{
		Use:   "auth",
		Short: "Log in and out of the slate API",
		Long: "Authenticate this machine against the identity provider using the OAuth 2.0\n" +
			"device authorization grant. The CLI prints a code and a URL; approve it in\n" +
			"any browser on any device, including from a different machine over SSH. The\n" +
			"resulting token is stored in the OS keychain, or in a file only this user\n" +
			"can read where there is none.\n\n" +
			"Where slate is pointed comes from one config file. `slate auth status` prints\n" +
			"its path and its api, issuer and client_id, and `slate auth login <url>` fills\n" +
			"in whatever it lacks.\n\n" +
			"With SLATE_CLIENT_SECRET set, slate authenticates as the service client the\n" +
			"config file's client_id names, through the client-credentials grant. It\n" +
			"requests a token per run and stores nothing, so login and logout refuse.",
	})
	splitReadingFromChanging(cmd)
	cmd.AddCommand(inGroup(groupChanging, withAPIFlag(newAuthLoginCommand()), newAuthLogoutCommand())...)
	cmd.AddCommand(inGroup(groupReading, withAPIFlag(newAuthStatusCommand()), withAPIFlag(newAuthTokenCommand()))...)
	return cmd
}

func newAuthLoginCommand() *cobra.Command {
	var clientID string
	cmd := &cobra.Command{
		Use:   "login [url]",
		Short: "Log in by approving a code in any browser",
		Long: "Log in to the API at url, or to the config file's api when url is omitted.\n" +
			"Whatever the file lacks is filled in: api from url, the issuer from the\n" +
			"API's RFC 9728 metadata, and the client id from --client-id or this\n" +
			"machine's short hostname. Each is printed with where it came from before\n" +
			"the login starts, and written to the file once it succeeds. A value already\n" +
			"in the file is never rewritten.",
		Example: "  slate auth login\n" +
			"  slate auth login https://slate.example.com",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if config.IsService() {
				return fmt.Errorf("%s is set, so slate authenticates as a service client with no login; unset it to log in as a person", config.ClientSecretEnv)
			}
			url := apiOverride
			if len(args) == 1 {
				if apiOverride != "" && apiOverride != args[0] {
					return goclikit.UsageError(fmt.Errorf("--api %s and the url %s name different APIs; give one", apiOverride, args[0]))
				}
				url = args[0]
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), loginTimeout)
			defer cancel()

			deployment, err := config.ForLogin(ctx, url, clientID)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Logging in to:")
			goclilogin.WriteDeployment(cmd.ErrOrStderr(), deployment)
			_, _ = fmt.Fprintln(cmd.ErrOrStderr())

			login := config.Login(deployment)
			store := goclilogin.NewTokenStore(login)

			// pkg/browser hands the launcher subprocess os.Stdout and os.Stderr,
			// which puts whatever the browser writes at startup — GPU warnings,
			// favicon decode failures, a dbus complaint — in the middle of the
			// user code and the verification URL. A launcher that fails is
			// reported by OpenURL's error instead, so nothing diagnostic is lost
			// by dropping the stream. Package-level vars are the library's only
			// knob for it.
			browser.Stdout = io.Discard
			browser.Stderr = io.Discard

			token, err := goclilogin.Login(ctx, login, func(p goclilogin.DevicePrompt) {
				goclilogin.WriteInstructions(cmd.ErrOrStderr(), login.ClientID, p)
				if openErr := browser.OpenURL(p.BrowserURL()); openErr != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "(could not open a browser here: %v)\n", openErr)
				}
			})
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					return fmt.Errorf("timed out waiting for approval after %s; run `slate auth login` again", loginTimeout)
				}
				return err
			}
			backend, err := store.Save(login.ClientID, token)
			if err != nil {
				return fmt.Errorf("save token: %w", err)
			}
			if backend == goclilogin.BackendFile {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"\nNo OS keyring on this host — the token is in %s, readable by this user only.\n",
					store.FilePath())
			}
			if err := deployment.Save(); err != nil {
				return fmt.Errorf("logged in as %s, but the config file was not written: %w", login.ClientID, err)
			}

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\n✓ Logged in (%s)\n", login.ClientID)
			return nil
		},
	}
	cmd.Flags().StringVar(&clientID, "client-id", "",
		"Log in as this OAuth client when the config file names none (default slate-cli-<short hostname>)")
	return cmd
}

func newAuthLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "logout",
		Short:   "Remove the stored token from this machine's keychain",
		Example: "  slate auth logout",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if config.IsService() {
				return fmt.Errorf("%s is set, so slate authenticates as a service client and stores no token to remove; unset it to log out as a person", config.ClientSecretEnv)
			}
			// Only the client id finds the token, so a file lacking the other
			// two keys still logs out. A file naming no client falls back to
			// slate-cli-<short hostname>, the id login uses when the file names
			// none, so a token stored by a login whose file was never written
			// is still removed. No other API is asked, so logout takes no --api.
			deployment, err := config.Load(cmd.Context(), "")
			if err != nil && !errors.Is(err, goclilogin.ErrNotConfigured) {
				return err
			}
			if deployment.HandsTokens() {
				return fmt.Errorf("%s sets token_command, so slate is handed its tokens and stores none to remove", deployment.Path)
			}
			if deployment.ClientID.Value == "" {
				deployment.ClientID.Value = config.DefaultClientID()
			}
			clientID := deployment.ClientID.Value
			if err := goclilogin.NewTokenStore(config.Login(deployment)).Delete(clientID); err != nil {
				if errors.Is(err, goclilogin.ErrNotLoggedIn) {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Not logged in — nothing to remove.")
					return nil
				}
				return fmt.Errorf("remove token from keychain: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Logged out (%s).\n", clientID)
			return nil
		},
	}
}

func newAuthTokenCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "token",
		Short: "Print the current access token to stdout",
		Long: "Print a valid access token to stdout, refreshing it first if it has\n" +
			"expired. Intended for scripting. Pass it to curl on stdin, as the example\n" +
			"does, so the token never appears in another process's arguments. Exits\n" +
			"non-zero if not logged in. With SLATE_CLIENT_SECRET set, it requests a new\n" +
			"service token instead, for every scope the client holds, and with\n" +
			"token_command in the config file it prints the token that command hands.",
		Example: "  slate auth token\n" +
			"  printf 'Authorization: Bearer %s\\n' \"$(slate auth token)\" | curl --header @- https://slate.example.com/api/v1/issues",
		Args:        cobra.NoArgs,
		Annotations: everyScopeTheClientHolds(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			deployment, err := config.Load(cmd.Context(), apiOverride)
			if err != nil {
				return err
			}
			source, err := tokenSource(cmd, deployment)
			if errors.Is(err, errNeedsLogin) {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Not logged in as %s. Run `slate auth login`.\n", deployment.ClientID.Value)
				return exitCode(1)
			}
			if err != nil {
				return err
			}
			token, err := source.Token()
			if err != nil && deployment.HandsTokens() {
				return fmt.Errorf("token_command in %s handed no token: %w", deployment.Path, err)
			}
			if err != nil && config.IsService() {
				return fmt.Errorf("obtain a token for service client %s: %w", deployment.ClientID.Value, err)
			}
			if err != nil {
				return fmt.Errorf("obtain a valid token (refresh may have failed — try `slate auth login`): %w", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), token.AccessToken)
			return nil
		},
	}
}

// credentialType is whose credential `auth status` checked, spelled as Google's
// credential files spell a person's OAuth login and a service's.
type credentialType string

const (
	// authorizedUser is the device-grant login a person made on this machine.
	authorizedUser credentialType = "authorized_user"
	// serviceAccount is the client-credentials grant SLATE_CLIENT_SECRET selects.
	serviceAccount credentialType = "service_account"
	// externalAccount is a token the config file's token_command hands, as
	// Google names a credential an executable supplies.
	externalAccount credentialType = "external_account"
)

// statusReport is the stable JSON schema for `auth status --json`. It reports
// what this machine holds, not what the token asserts: the claims are the API's
// to verify against the provider's JWKS, and a local reading of them would only say
// what an unverified token claims about itself.
//
// For a service, logged_in is true only when the provider granted a token just
// now. A service stores none, so nothing else says the next command will work.
type statusReport struct {
	LoggedIn bool `json:"logged_in"`
	// Configured is false when the config file lacks a value, which ConfigFile
	// names and the empty fields below show.
	Configured bool                    `json:"configured"`
	ConfigFile string                  `json:"config_file"`
	Type       credentialType          `json:"type"`
	API        string                  `json:"api"`
	ClientID   string                  `json:"client_id"`
	Issuer     string                  `json:"issuer"`
	ExpiresAt  string                  `json:"expires_at,omitempty"`
	Expired    bool                    `json:"expired"`
	Session    goclilogin.SessionState `json:"session"`

	// Backend is where the token is kept. A host with no OS keyring stores it
	// in a mode-600 file instead, and that is worth saying out loud.
	Backend goclilogin.Backend `json:"backend,omitempty"`

	// KeyringNote carries the keyring's own failure when there is no token and
	// the keyring is the reason, rather than simply holding no entry.
	KeyringNote string `json:"keyring_note,omitempty"`

	// Scopes are the service scopes the provider granted the client, each
	// asked for alone. A person's login reaches every route, so it has none.
	Scopes []string `json:"scopes,omitempty"`

	// holdsNoScope is a service whose secret the provider accepted while it
	// refused every scope, which a new secret would not fix.
	holdsNoScope bool
}

func newAuthStatusCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show where the CLI is pointed and whether it is logged in",
		Long: "Show the api, issuer and client_id this machine uses, where each came\n" +
			"from, the config file's path, and whether a token is stored for the client.\n" +
			"A service client is asked a grant per scope, and the scopes it holds are\n" +
			"listed. Exits 1 when the file lacks a value, no token is stored, or the\n" +
			"provider rejected the session or every scope.",
		Example:     "  slate auth status\n  slate auth status --json",
		Args:        cobra.NoArgs,
		Annotations: everyScopeTheClientHolds(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			deployment, loadErr := config.Load(cmd.Context(), apiOverride)
			if loadErr != nil && !errors.Is(loadErr, goclilogin.ErrNotConfigured) {
				return loadErr
			}
			report := statusReport{
				Configured: loadErr == nil,
				ConfigFile: deployment.Path,
				Type:       authorizedUser,
				API:        deployment.API.Value,
				ClientID:   deployment.ClientID.Value,
				Issuer:     deployment.Issuer.Value,
			}
			switch {
			case deployment.HandsTokens():
				report.Type = externalAccount
			case config.IsService():
				report.Type = serviceAccount
			}
			switch {
			case !report.Configured:
			case report.Type == externalAccount:
				source, err := goclilogin.CommandTokenSource(cmd.Context(), deployment.TokenCommand.Argv())
				grantedStatus(source, err, &report)
			case report.Type == serviceAccount:
				serviceStatus(cmd.Context(), deployment, &report)
			default:
				if err := loginStatus(cmd.Context(), deployment, &report); err != nil {
					return err
				}
			}

			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(report); err != nil {
					return err
				}
			} else {
				printStatus(cmd.OutOrStdout(), report, deployment, loadErr)
			}

			// A rejected session exits non-zero for the same reason being logged
			// out does: nothing the caller runs next will work until they log in.
			// An unverified login exits zero, because its stored token may still
			// work. An unverified service or handed token exits 1 through
			// logged_in, which grantedStatus sets only for a live session.
			if !report.Configured || !report.LoggedIn || report.Session == goclilogin.SessionRejected {
				return exitCode(1)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output status as JSON to stdout")
	return cmd
}

// loginStatus fills report from the token this machine logged in for.
func loginStatus(ctx context.Context, d goclilogin.Deployment, report *statusReport) error {
	login := config.Login(d)
	store := goclilogin.NewTokenStore(login)
	token, backend, err := store.Load(d.ClientID.Value)
	switch {
	case errors.Is(err, goclilogin.ErrNotLoggedIn):
		// Logged out is a valid state, reported on stdout, exit 1 (like gh).
		// goclilogin wraps ErrNotLoggedIn with the keyring's own failure when
		// there was one, and that distinction decides what the user should do:
		// a host with no Secret Service is fixed by logging in, a locked
		// keychain is not.
		if detail := strings.TrimPrefix(err.Error(), goclilogin.ErrNotLoggedIn.Error()); detail != "" {
			report.KeyringNote = strings.Trim(detail, " ()")
		}
	case err != nil:
		return fmt.Errorf("read token from keychain: %w", err)
	default:
		report.LoggedIn = true
		report.Backend = backend
		if !token.Expiry.IsZero() {
			report.ExpiresAt = token.Expiry.Format(time.RFC3339)
			report.Expired = time.Now().After(token.Expiry)
		}
		// Ask for a token the way a resource command does, which is the
		// only thing that separates a soft expiry from a revoked grant.
		// VerifySession reads the issuer's discovery document first, so an
		// unreachable issuer reports unverified even while the stored
		// access token is live. A live token then answers with no further
		// call, and an expired one performs the refresh the next call would
		// have performed anyway.
		report.Session, token = goclilogin.VerifySession(ctx, login, store)
		if token != nil && !token.Expiry.IsZero() {
			report.ExpiresAt = token.Expiry.Format(time.RFC3339)
			report.Expired = time.Now().After(token.Expiry)
		}
	}
	return nil
}

// grantedStatus asks source for a token, because a service or a handed token
// stores none that could say whether the next command will get one.
func grantedStatus(source oauth2.TokenSource, err error, report *statusReport) {
	var token *oauth2.Token
	if err == nil {
		token, err = source.Token()
	}
	grantedToken(token, err, report)
}

// serviceStatus asks the provider for each of slate's scopes, because a service
// client holds only the ones its commands need, and a grant naming one it lacks
// is refused whole.
func serviceStatus(ctx context.Context, d goclilogin.Deployment, report *statusReport) {
	held, token, err := heldScopes(ctx, d)
	report.Scopes = held
	var refused *scopesRefused
	if errors.As(err, &refused) {
		report.Session, report.holdsNoScope = goclilogin.SessionRejected, true
		return
	}
	grantedToken(token, err, report)
}

func grantedToken(token *oauth2.Token, err error, report *statusReport) {
	report.Session, token = goclilogin.ClassifySession(token, err)
	report.LoggedIn = report.Session == goclilogin.SessionLive
	if token != nil && !token.Expiry.IsZero() {
		report.ExpiresAt = token.Expiry.Format(time.RFC3339)
	}
}

// printStatus leads with the verdict on a line of its own, then prints where
// the CLI is pointed. A script checking a login reads the first line alone.
//
// WriteDeployment's widest key is client_id, padded by two, so the rows after
// it pad their key to the column its values start in.
func printStatus(out io.Writer, r statusReport, d goclilogin.Deployment, loadErr error) {
	switch {
	case !r.Configured:
		_, _ = fmt.Fprintf(out, "%v\n", loadErr)
	case r.Type == externalAccount && r.LoggedIn:
		_, _ = fmt.Fprintln(out, "Handed a token by token_command")
	case r.Type == externalAccount:
		_, _ = fmt.Fprintf(out, "token_command handed no token. Check what %q asks.\n", d.TokenCommand)
	case r.Type == serviceAccount && r.holdsNoScope:
		_, _ = fmt.Fprintf(out, "Service client holds none of slate's scopes at %s. Register the ones its commands need; each command's --help names them.\n", r.Issuer)
	case r.Type == serviceAccount && r.Session == goclilogin.SessionRejected:
		_, _ = fmt.Fprintf(out, "Service client refused by %s. Check client_id in the config file, and %s.\n", r.Issuer, config.ClientSecretEnv)
	case r.Type == serviceAccount && r.Session == goclilogin.SessionUnverified:
		_, _ = fmt.Fprintf(out, "Service client configured; %s could not be reached.\n", r.Issuer)
	case r.Type == serviceAccount:
		_, _ = fmt.Fprintln(out, "Service client (client credentials)")
	case !r.LoggedIn:
		_, _ = fmt.Fprintf(out, "Not logged in as %s. Run `slate auth login` to authenticate.\n", r.ClientID)
	case r.Session == goclilogin.SessionRejected:
		_, _ = fmt.Fprintf(out, "Session rejected by %s. Run `slate auth login` to authenticate.\n", r.Issuer)
	default:
		_, _ = fmt.Fprintln(out, "Logged in")
	}
	goclilogin.WriteDeployment(out, d)
	if r.KeyringNote != "" {
		_, _ = fmt.Fprintf(out, "  %-11s%s\n", "note", r.KeyringNote)
	}
	if r.ExpiresAt != "" {
		state := "valid"
		switch {
		case r.Type == serviceAccount, r.Type == externalAccount:
			state = "granted"
		case r.Expired:
			state = "expired — will refresh on next use"
		}
		_, _ = fmt.Fprintf(out, "  %-11s%s (expires %s)\n", "token", state, r.ExpiresAt)
	}
	if len(r.Scopes) > 0 {
		_, _ = fmt.Fprintf(out, "  %-11s%s\n", "scopes", strings.Join(r.Scopes, " "))
	}
	if r.Backend == goclilogin.BackendFile {
		_, _ = fmt.Fprintf(out, "  %-11s%s — no OS keyring on this host\n", "storage", r.Backend)
	}
	if r.Type != serviceAccount && r.Session == goclilogin.SessionUnverified {
		_, _ = fmt.Fprintf(out, "  %-11sunverified — %s could not be reached\n", "session", r.Issuer)
	}
}
