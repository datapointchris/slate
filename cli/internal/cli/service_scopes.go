package cli

import (
	"cmp"
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
)

// serviceScopes is every scope the API lists, in the order help and status print them.
var serviceScopes = []string{
	scopeRecordsRead, scopeRecordsPlan, scopeClaimsHold, scopeIssuesClose,
	scopeCommentsWrite, scopeWorklogsWrite, scopeSessionsWrite,
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
	scopes := grant.scopes
	if grant.held {
		held, _, err := heldScopes(cmd.Context(), d)
		if err != nil {
			return nil, err
		}
		scopes = held
	}
	if len(scopes) == 0 {
		return nil, fmt.Errorf("no service scope reaches %#q, so only a person can run it", cmd.CommandPath())
	}
	source, err := goclilogin.ClientCredentialsTokenSource(cmd.Context(), config.Service(d, scopes), config.ClientSecret())
	if err != nil {
		return nil, err
	}
	return scopesNamedOnRefusal{source: source, clientID: d.ClientID.Value, issuer: d.Issuer.Value, command: cmd.CommandPath(), scopes: scopes}, nil
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
	var retrieve *oauth2.RetrieveError
	if errors.As(err, &retrieve) && retrieve.ErrorCode == "invalid_scope" {
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

// heldScopes asks the provider for each of slate's scopes in a grant of its own,
// and returns the ones it granted with one of the tokens granting them. One
// grant could not say which: a provider refuses a whole request naming a scope
// the client lacks, as Authelia does, or narrows the token and names what it
// granted. A refusal that is not invalid_scope is about the client or the
// provider rather than a scope, and comes back as the error.
func heldScopes(ctx context.Context, d goclilogin.Deployment) ([]string, *oauth2.Token, error) {
	type answer struct {
		token *oauth2.Token
		err   error
	}
	answers := make([]answer, len(serviceScopes))
	var wg sync.WaitGroup
	for i, scope := range serviceScopes {
		wg.Go(func() {
			source, err := goclilogin.ClientCredentialsTokenSource(ctx, config.Service(d, []string{scope}), config.ClientSecret())
			if err == nil {
				answers[i].token, err = source.Token()
			}
			answers[i].err = err
		})
	}
	wg.Wait()

	var (
		held    []string
		granted *oauth2.Token
		refusal error
	)
	for i, a := range answers {
		var retrieve *oauth2.RetrieveError
		switch {
		case errors.As(a.err, &retrieve) && retrieve.ErrorCode == "invalid_scope":
			refusal = cmp.Or(refusal, a.err)
		case a.err != nil:
			return nil, nil, a.err
		case len(scopesNotGranted(a.token, serviceScopes[i:i+1])) == 0:
			held = append(held, serviceScopes[i])
			granted = cmp.Or(granted, a.token)
		}
	}
	if len(held) == 0 {
		return nil, nil, &scopesRefused{clientID: d.ClientID.Value, issuer: d.Issuer.Value, scopes: serviceScopes, cause: refusal}
	}
	return held, granted, nil
}
