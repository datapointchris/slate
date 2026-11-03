package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/datapointchris/goclilogin"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/datapointchris/slate/cli/internal/config"
)

// The scopes the API lists for a service client. src/slate/api/client_scopes.py
// holds the routes each one reaches.
const (
	scopeRecordsRead   = "slate.records.read"
	scopeRecordsPlan   = "slate.records.plan"
	scopeClaimsHold    = "slate.claims.hold"
	scopeIssuesClose   = "slate.issues.close"
	scopeCommentsWrite = "slate.comments.write"
	scopeWorklogsWrite = "slate.worklogs.write"
	scopeSessionsWrite = "slate.sessions.write"
	// scopeWorklogsObserve joins a session to a worklog another client opened.
	// No command declares it. A job holding it reaches `worklogs join` on a
	// token its token_command hands, which asks no grant.
	scopeWorklogsObserve = "slate.worklogs.observe"
)

// serviceScopes is every scope the API lists, in the order auth status answers for them.
var serviceScopes = []string{
	scopeRecordsRead, scopeRecordsPlan, scopeClaimsHold, scopeIssuesClose,
	scopeCommentsWrite, scopeWorklogsWrite, scopeSessionsWrite, scopeWorklogsObserve,
}

// serviceScopesAnnotation is what a command asks a service grant for. Its value
// is the scopes, space-separated, or empty where no scope reaches the command's
// routes, or heldScopesValue. A command that reaches no API carries none.
const serviceScopesAnnotation = "slate.service-scopes"

// heldScopesValue marks a command asking for every scope its client holds.
const heldScopesValue = "held"

// scopesForService declares the scopes a command's routes need, as the
// Annotations of its literal. A service grant asks for these and no others.
func scopesForService(scopes ...string) map[string]string {
	return map[string]string{serviceScopesAnnotation: strings.Join(scopes, " ")}
}

// personOnly declares a command that sends a route no scope reaches, or a body
// the API refuses every scoped client. A service is refused it before any grant.
func personOnly() map[string]string {
	return map[string]string{serviceScopesAnnotation: ""}
}

// everyScopeTheClientHolds declares a command about the credential itself, which
// asks for whatever the client holds rather than what one route needs.
func everyScopeTheClientHolds() map[string]string {
	return map[string]string{serviceScopesAnnotation: heldScopesValue}
}

// serviceGrant is a command's declaration, read back.
type serviceGrant struct {
	scopes []string
	held   bool
}

// declaredGrant reads cmd's declaration, and reports whether it made one.
func declaredGrant(cmd *cobra.Command) (serviceGrant, bool) {
	value, declared := cmd.Annotations[serviceScopesAnnotation]
	if value == heldScopesValue {
		return serviceGrant{held: true}, declared
	}
	return serviceGrant{scopes: strings.Fields(value)}, declared
}

// showServiceScopesInHelp ends every declaring command's help with its scopes,
// so whoever registers a service client reads them from the binary it runs.
func showServiceScopesInHelp(root *cobra.Command) {
	help := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		help(cmd, args)
		grant, declared := declaredGrant(cmd)
		if !declared {
			return
		}
		line := strings.Join(grant.scopes, " ")
		switch {
		case grant.held:
			line = "every scope the client holds, each asked for in a grant of its own"
		case len(grant.scopes) == 0:
			line = "none reaches this command, so only a person can run it"
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nService scopes:\n  %s\n", line)
	})
}

// serviceTokenSource is the client-credentials grant for the scopes cmd declares.
func serviceTokenSource(cmd *cobra.Command, d goclilogin.Deployment, grant serviceGrant) (oauth2.TokenSource, error) {
	switch {
	case grant.held:
		return heldTokenSource(cmd, d)
	case len(grant.scopes) == 0:
		return nil, fmt.Errorf("no service scope reaches %#q, so only a person can run it", cmd.CommandPath())
	}
	return scopedTokenSource(cmd, d, grant.scopes)
}

func scopedTokenSource(cmd *cobra.Command, d goclilogin.Deployment, scopes []string) (oauth2.TokenSource, error) {
	source, err := goclilogin.ClientCredentialsTokenSource(cmd.Context(), config.Service(d, scopes), config.ClientSecret())
	if err != nil {
		return nil, err
	}
	return scopesNamedOnRefusal{source: source, clientID: d.ClientID.Value, issuer: d.Issuer.Value, command: cmd.CommandPath(), scopes: scopes}, nil
}

// heldTokenSource is a grant for every scope the probe finds the client holds.
// Where it holds one, the probe's own token is the answer. A scope the provider
// gave no answer for is left out, and named on stderr.
func heldTokenSource(cmd *cobra.Command, d goclilogin.Deployment) (oauth2.TokenSource, error) {
	answers := probeScopes(cmd.Context(), d)
	held := scopesAnswered(answers, answerGranted)
	if len(held) == 0 {
		return nil, probeFailure(d, answers)
	}
	if unknown := scopesAnswered(answers, answerUnknown); len(unknown) > 0 {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: the token leaves out %s, which %s gave no answer for (%s)\n",
			strings.Join(unknown, " "), d.Issuer.Value, answers[slices.IndexFunc(answers, isUnknown)].reason())
	}
	if len(held) == 1 {
		return oauth2.StaticTokenSource(answers[slices.IndexFunc(answers, isGranted)].token), nil
	}
	return scopedTokenSource(cmd, d, held)
}

// probeFailure is why a probe found no scope the client holds: the provider's
// answer about the client or itself where one came, else every scope refused.
func probeFailure(d goclilogin.Deployment, answers []scopeAnswer) error {
	i := slices.IndexFunc(answers, isUnknown)
	switch {
	case i < 0:
		return &scopesRefused{clientID: d.ClientID.Value, issuer: d.Issuer.Value, scopes: serviceScopes, cause: answers[0].err}
	case goclilogin.IsSessionRejected(answers[i].err):
		return fmt.Errorf("%w (%w)", refusedServiceClient(d), answers[i].err)
	}
	return fmt.Errorf("obtain a token for service client %s from %s: %w", d.ClientID.Value, d.Issuer.Value, answers[i].err)
}

// scopesNamedOnRefusal turns the provider refusing or narrowing the scopes a
// command asked for into an error naming them. The provider's own refusal names
// a URL and one scope, and a narrowed token would answer 403 route by route.
type scopesNamedOnRefusal struct {
	source   oauth2.TokenSource
	clientID string
	issuer   string
	command  string
	scopes   []string
}

func (s scopesNamedOnRefusal) Token() (*oauth2.Token, error) {
	token, err := s.source.Token()
	if isScopeRefusal(err) {
		return nil, &scopesRefused{clientID: s.clientID, issuer: s.issuer, command: s.command, scopes: s.scopes, cause: err}
	}
	if err != nil {
		return nil, err
	}
	if missing := scopesNotGranted(token, s.scopes); len(missing) > 0 {
		return nil, &scopesRefused{clientID: s.clientID, issuer: s.issuer, command: s.command, scopes: missing}
	}
	return token, nil
}

// scopesNotGranted is what a token's response says it left out of asked. RFC
// 6749 §5.1 has a provider that grants fewer scopes than asked name the ones it
// granted, an empty list included. A response without the field granted every
// scope asked.
func scopesNotGranted(token *oauth2.Token, asked []string) []string {
	named, ok := token.Extra("scope").(string)
	if !ok {
		return nil
	}
	granted := strings.Fields(named)
	var missing []string
	for _, scope := range asked {
		if !slices.Contains(granted, scope) {
			missing = append(missing, scope)
		}
	}
	return missing
}

// scopesRefused is a service client denied scopes it asked for: a whole
// request refused with invalid_scope, or a token granted without them.
type scopesRefused struct {
	clientID string
	issuer   string
	// command is the command that asked, or empty for the probe of every scope.
	command string
	scopes  []string
	cause   error
}

func (e *scopesRefused) Error() string {
	if e.command == "" {
		return fmt.Sprintf("%s grants service client %s none of slate's scopes; register the ones its commands need, which each command's --help names",
			e.issuer, e.clientID)
	}
	message := fmt.Sprintf("%s did not grant service client %s %s, which %#q asks for; register them on the client, and `slate auth status` lists the ones it holds",
		e.issuer, e.clientID, strings.Join(e.scopes, " "), e.command)
	var retrieve *oauth2.RetrieveError
	if errors.As(e.cause, &retrieve) && retrieve.ErrorDescription != "" {
		message += " (" + retrieve.ErrorDescription + ")"
	}
	return message
}

func (e *scopesRefused) Unwrap() error { return e.cause }

func isScopeRefusal(err error) bool {
	var retrieve *oauth2.RetrieveError
	return errors.As(err, &retrieve) && retrieve.ErrorCode == "invalid_scope"
}

// grantAnswer is whether the client holds one scope, as its probe found.
type grantAnswer string

const (
	// answerGranted is a token carrying the scope.
	answerGranted grantAnswer = "granted"
	// answerRefused is the scope refused with invalid_scope, or a token
	// granted without it.
	answerRefused grantAnswer = "refused"
	// answerUnknown is an answer about the client or the provider rather than
	// the scope, such as a refused secret or a 5xx, or no answer at all.
	answerUnknown grantAnswer = "unknown"
)

// scopeAnswer is the provider's answer to the client asking for one scope
// alone. Session reads the request as goclilogin reads any grant, so a refused
// secret and a refused scope are both rejected, and Grant tells them apart.
type scopeAnswer struct {
	Scope   string                  `json:"scope"`
	Grant   grantAnswer             `json:"grant"`
	Session goclilogin.SessionState `json:"session"`
	// Error is what the provider answered where it granted no token carrying
	// the scope.
	Error string `json:"error,omitempty"`

	token *oauth2.Token
	err   error
}

// reason is Error's first line. x/oauth2 follows a status with no error code
// by the response body, which would break a row.
func (a scopeAnswer) reason() string {
	line, _, _ := strings.Cut(a.Error, "\n")
	return line
}

func isGranted(a scopeAnswer) bool { return a.Grant == answerGranted }
func isUnknown(a scopeAnswer) bool { return a.Grant == answerUnknown }

// scopesAnswered is the scopes whose probe answered grant, in answers' order.
func scopesAnswered(answers []scopeAnswer, grant grantAnswer) []string {
	var scopes []string
	for _, a := range answers {
		if a.Grant == grant {
			scopes = append(scopes, a.Scope)
		}
	}
	return scopes
}

// probeScopes asks the provider for each of slate's scopes in a grant of its
// own, concurrently. One grant could not say which the client holds: a provider
// refuses a whole request naming a scope the client lacks, as Authelia does,
// or narrows the token and names what it granted. Each probe is a discovery
// read and a token request, each bounded by goclilogin's request timeout, and
// one failing leaves the others' answers standing.
func probeScopes(ctx context.Context, d goclilogin.Deployment) []scopeAnswer {
	answers := make([]scopeAnswer, len(serviceScopes))
	var wg sync.WaitGroup
	for i, scope := range serviceScopes {
		wg.Go(func() { answers[i] = probeScope(ctx, d, scope) })
	}
	wg.Wait()
	return answers
}

func probeScope(ctx context.Context, d goclilogin.Deployment, scope string) scopeAnswer {
	source, err := goclilogin.ClientCredentialsTokenSource(ctx, config.Service(d, []string{scope}), config.ClientSecret())
	var token *oauth2.Token
	if err == nil {
		token, err = source.Token()
	}
	answer := scopeAnswer{Scope: scope, err: err}
	switch {
	case isScopeRefusal(err):
		answer.Grant, answer.Session = answerRefused, goclilogin.SessionRejected
	case err != nil:
		answer.Grant = answerUnknown
		answer.Session, _ = goclilogin.ClassifySession(nil, err)
	case len(scopesNotGranted(token, []string{scope})) > 0:
		answer.Grant, answer.Session = answerRefused, goclilogin.SessionRejected
		answer.err = fmt.Errorf("granted a token for %q instead", token.Extra("scope"))
	default:
		answer.Grant, answer.Session, answer.token = answerGranted, goclilogin.SessionLive, token
	}
	if answer.err != nil {
		answer.Error = answer.err.Error()
	}
	return answer
}
